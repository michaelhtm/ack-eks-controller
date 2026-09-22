// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package cluster

import (
	"errors"
	"fmt"
	"testing"

	ackerr "github.com/aws-controllers-k8s/runtime/pkg/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aws-controllers-k8s/eks-controller/apis/v1alpha1"
)

func compute(enabled *bool, nodePools ...string) *v1alpha1.ComputeConfigRequest {
	c := &v1alpha1.ComputeConfigRequest{Enabled: enabled}
	for _, np := range nodePools {
		c.NodePools = append(c.NodePools, aws.String(np))
	}
	return c
}

func storage(enabled *bool) *v1alpha1.StorageConfigRequest {
	return &v1alpha1.StorageConfigRequest{BlockStorage: &v1alpha1.BlockStorage{Enabled: enabled}}
}

func clusterWithAutoMode(c *v1alpha1.ComputeConfigRequest, s *v1alpha1.StorageConfigRequest, loadBalancing *bool) *resource {
	spec := v1alpha1.ClusterSpec{
		Name:          aws.String("test-cluster"),
		ComputeConfig: c,
		StorageConfig: s,
		KubernetesNetworkConfig: &v1alpha1.KubernetesNetworkConfigRequest{
			IPFamily:        aws.String("ipv4"),
			ServiceIPv4CIDR: aws.String("172.20.0.0/16"),
		},
	}
	if loadBalancing != nil {
		spec.KubernetesNetworkConfig.ElasticLoadBalancing = &v1alpha1.ElasticLoadBalancing{Enabled: loadBalancing}
	}
	return &resource{ko: &v1alpha1.Cluster{Spec: spec}}
}

func TestNewAutoModeUpdateInputSendsUniformTuple(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		desired := clusterWithAutoMode(compute(aws.Bool(enabled)), storage(aws.Bool(enabled)), aws.Bool(enabled))

		input := newAutoModeUpdateInput(desired)

		require.NotNil(t, input.ComputeConfig)
		require.NotNil(t, input.StorageConfig)
		require.NotNil(t, input.StorageConfig.BlockStorage)
		require.NotNil(t, input.KubernetesNetworkConfig)
		require.NotNil(t, input.KubernetesNetworkConfig.ElasticLoadBalancing)

		assert.Equal(t, aws.Bool(enabled), input.ComputeConfig.Enabled)
		assert.Equal(t, aws.Bool(enabled), input.StorageConfig.BlockStorage.Enabled)
		assert.Equal(t, aws.Bool(enabled), input.KubernetesNetworkConfig.ElasticLoadBalancing.Enabled)
	}
}

func TestNewAutoModeUpdateInputOmitsImmutableNetworkFields(t *testing.T) {
	desired := clusterWithAutoMode(compute(aws.Bool(true)), storage(aws.Bool(true)), aws.Bool(true))

	input := newAutoModeUpdateInput(desired)

	assert.Empty(t, input.KubernetesNetworkConfig.IpFamily)
	assert.Nil(t, input.KubernetesNetworkConfig.ServiceIpv4Cidr)
}

func TestNewResourceDeltaAutoMode(t *testing.T) {
	tests := []struct {
		name      string
		desired   *resource
		latest    *resource
		different bool
	}{
		{
			"nothing declared, AWS reports the Auto Mode defaults",
			clusterWithAutoMode(nil, nil, nil),
			clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)),
			false,
		},
		{
			"nothing declared, AWS omits compute and storage",
			clusterWithAutoMode(nil, nil, nil),
			clusterWithAutoMode(nil, nil, aws.Bool(false)),
			false,
		},
		{
			"only storage declared, AWS reports the rest",
			clusterWithAutoMode(nil, storage(aws.Bool(false)), nil),
			clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)),
			false,
		},
		{
			"enable requested against a non Auto Mode cluster still differs",
			clusterWithAutoMode(compute(aws.Bool(true)), storage(aws.Bool(true)), aws.Bool(true)),
			clusterWithAutoMode(nil, nil, aws.Bool(false)),
			true,
		},
		{
			"disable requested against an Auto Mode cluster still differs",
			clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)),
			clusterWithAutoMode(compute(aws.Bool(true)), storage(aws.Bool(true)), aws.Bool(true)),
			true,
		},
		{
			"nodePools change on an Auto Mode cluster still differs",
			clusterWithAutoMode(compute(aws.Bool(true), "general-purpose"), storage(aws.Bool(true)), aws.Bool(true)),
			clusterWithAutoMode(compute(aws.Bool(true), "system", "general-purpose"), storage(aws.Bool(true)), aws.Bool(true)),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta := newResourceDelta(tt.desired, tt.latest)

			autoModeDiff := delta.DifferentAt("Spec.ComputeConfig") ||
				delta.DifferentAt("Spec.StorageConfig") ||
				delta.DifferentAt("Spec.KubernetesNetworkConfig.ElasticLoadBalancing")
			assert.Equal(t, tt.different, autoModeDiff, "differences: %v", delta.Differences)
		})
	}
}

func TestNewAutoModeUpdateInputWithoutComputeConfig(t *testing.T) {
	desired := clusterWithAutoMode(nil, storage(aws.Bool(false)), aws.Bool(false))

	input := newAutoModeUpdateInput(desired)

	require.NotNil(t, input.ComputeConfig)
	assert.Nil(t, input.ComputeConfig.Enabled)
	assert.Nil(t, input.ComputeConfig.NodePools)
	assert.Nil(t, input.ComputeConfig.NodeRoleArn)
}

func TestAutoModeTerminalError(t *testing.T) {
	tupleMismatch := &smithy.GenericAPIError{
		Code:    "InvalidParameterException",
		Message: "For EKS Auto Mode, please ensure that all required configs, including computeConfig, kubernetesNetworkConfig, and blockStorage are all either fully enabled or fully disabled.",
	}
	rolePropagating := &smithy.GenericAPIError{
		Code:    "ClientException",
		Message: "The provided role doesn't have the Amazon EKS Managed Policies associated with it.",
	}

	var termErr *ackerr.TerminalError

	wrapped := autoModeTerminalError(fmt.Errorf("failed to update AutoMode config: %w", tupleMismatch))
	assert.ErrorAs(t, wrapped, &termErr, "a rejected Auto Mode request needs a spec change, so it is terminal")
	assert.Contains(t, wrapped.Error(), "fully enabled or fully disabled")

	assert.NotErrorAs(t, autoModeTerminalError(fmt.Errorf("wrapped: %w", rolePropagating)), &termErr,
		"IAM propagation is retriable and must stay recoverable")
	assert.NotErrorAs(t, autoModeTerminalError(errors.New("boom")), &termErr,
		"a non-AWS error must not be reclassified")
}
