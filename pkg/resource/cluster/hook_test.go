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
	"testing"

	ackerr "github.com/aws-controllers-k8s/runtime/pkg/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aws-controllers-k8s/eks-controller/apis/v1alpha1"
)

func autoModeCluster(compute *bool, storage *bool, loadBalancing *bool) *resource {
	spec := v1alpha1.ClusterSpec{Name: aws.String("test-cluster")}
	if compute != nil {
		spec.ComputeConfig = &v1alpha1.ComputeConfigRequest{Enabled: compute}
	}
	if storage != nil {
		spec.StorageConfig = &v1alpha1.StorageConfigRequest{
			BlockStorage: &v1alpha1.BlockStorage{Enabled: storage},
		}
	}
	spec.KubernetesNetworkConfig = &v1alpha1.KubernetesNetworkConfigRequest{
		IPFamily:        aws.String("ipv4"),
		ServiceIPv4CIDR: aws.String("10.100.0.0/16"),
	}
	if loadBalancing != nil {
		spec.KubernetesNetworkConfig.ElasticLoadBalancing = &v1alpha1.ElasticLoadBalancing{
			Enabled: loadBalancing,
		}
	}
	return &resource{ko: &v1alpha1.Cluster{Spec: spec}}
}

func TestNewAutoModeUpdateInputSendsUniformTuple(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		desired := autoModeCluster(aws.Bool(enabled), aws.Bool(enabled), aws.Bool(enabled))

		input, err := newAutoModeUpdateInput(desired)

		require.NoError(t, err)
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
	desired := autoModeCluster(aws.Bool(true), aws.Bool(true), aws.Bool(true))

	input, err := newAutoModeUpdateInput(desired)

	require.NoError(t, err)
	assert.Empty(t, input.KubernetesNetworkConfig.IpFamily)
	assert.Nil(t, input.KubernetesNetworkConfig.ServiceIpv4Cidr)
}

func TestNewAutoModeUpdateInputRejectsIncompleteTuple(t *testing.T) {
	tests := []struct {
		name                            string
		compute, storage, loadBalancing *bool
	}{
		{"compute only", aws.Bool(true), nil, nil},
		{"missing storage", aws.Bool(true), nil, aws.Bool(true)},
		{"missing load balancing", aws.Bool(true), aws.Bool(true), nil},
		{"missing compute", nil, aws.Bool(false), aws.Bool(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := autoModeCluster(tt.compute, tt.storage, tt.loadBalancing)

			input, err := newAutoModeUpdateInput(desired)

			assert.Nil(t, input)
			var termErr *ackerr.TerminalError
			assert.ErrorAs(t, err, &termErr)
		})
	}
}

func TestNewAutoModeUpdateInputRejectsDisagreeingTuple(t *testing.T) {
	tests := []struct {
		name                            string
		compute, storage, loadBalancing *bool
	}{
		{"storage disagrees", aws.Bool(true), aws.Bool(false), aws.Bool(true)},
		{"load balancing disagrees", aws.Bool(false), aws.Bool(false), aws.Bool(true)},
		{"compute disagrees", aws.Bool(false), aws.Bool(true), aws.Bool(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := autoModeCluster(tt.compute, tt.storage, tt.loadBalancing)

			input, err := newAutoModeUpdateInput(desired)

			assert.Nil(t, input)
			var termErr *ackerr.TerminalError
			assert.ErrorAs(t, err, &termErr)
		})
	}
}

func clusterWithAutoMode(compute *v1alpha1.ComputeConfigRequest, storage *v1alpha1.StorageConfigRequest, loadBalancing *bool) *resource {
	spec := v1alpha1.ClusterSpec{
		Name:          aws.String("test-cluster"),
		ComputeConfig: compute,
		StorageConfig: storage,
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

func TestNewResourceDeltaAutoModeDisabledEqualsAbsent(t *testing.T) {
	tests := []struct {
		name      string
		desired   *resource
		latest    *resource
		different bool
	}{
		{
			"declared all false, AWS omits compute and storage",
			clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)),
			clusterWithAutoMode(nil, nil, aws.Bool(false)),
			false,
		},
		{
			"declared false with leftover nodePools, AWS omits compute and storage",
			clusterWithAutoMode(compute(aws.Bool(false), "general-purpose"), storage(aws.Bool(false)), aws.Bool(false)),
			clusterWithAutoMode(nil, nil, aws.Bool(false)),
			false,
		},
		{
			"declared empty structs, AWS omits compute and storage",
			clusterWithAutoMode(&v1alpha1.ComputeConfigRequest{}, &v1alpha1.StorageConfigRequest{}, aws.Bool(false)),
			clusterWithAutoMode(nil, nil, aws.Bool(false)),
			false,
		},
		{
			"AWS omits only storage",
			clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)),
			clusterWithAutoMode(compute(aws.Bool(false)), nil, aws.Bool(false)),
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

func TestAutoModeRequested(t *testing.T) {
	plain := clusterWithAutoMode(nil, nil, aws.Bool(false))

	assert.False(t, autoModeRequested(clusterWithAutoMode(nil, nil, nil), plain),
		"a spec declaring only sibling network fields must not drive an Auto Mode update")
	assert.False(t, autoModeRequested(clusterWithAutoMode(compute(aws.Bool(false)), storage(aws.Bool(false)), aws.Bool(false)), plain))
	assert.True(t, autoModeRequested(clusterWithAutoMode(compute(aws.Bool(true)), nil, nil), plain))
	assert.True(t, autoModeRequested(clusterWithAutoMode(nil, nil, nil),
		clusterWithAutoMode(compute(aws.Bool(true)), storage(aws.Bool(true)), aws.Bool(true))))
}
