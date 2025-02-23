/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package state

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1 "k8s.io/client-go/listers/core/v1"
	kubeclient "knative.dev/pkg/client/injection/kube/client"

	duckv1alpha1 "knative.dev/eventing/pkg/apis/duck/v1alpha1"
	listers "knative.dev/eventing/pkg/reconciler/testing/v1"
	"knative.dev/eventing/pkg/scheduler"
	tscheduler "knative.dev/eventing/pkg/scheduler/testing"
)

const (
	testNs   = "test-ns"
	sfsName  = "statefulset-name"
	vpodName = "vpod-name"
	vpodNs   = "vpod-ns"
)

func TestStateBuilder(t *testing.T) {
	testCases := []struct {
		name            string
		replicas        int32
		pendingReplicas int32
		vpods           [][]duckv1alpha1.Placement
		expected        State
		freec           int32
		err             error
	}{
		{
			name:     "no vpods",
			replicas: int32(0),
			vpods:    [][]duckv1alpha1.Placement{},
			expected: State{Capacity: 10, FreeCap: []int32{}, SchedulablePods: []int32{}, LastOrdinal: -1, StatefulSetName: sfsName, Pending: map[types.NamespacedName]int32{}, ExpectedVReplicaByVPod: map[types.NamespacedName]int32{}},
			freec:    int32(0),
		},
		{
			name:     "one vpods",
			replicas: int32(1),
			vpods:    [][]duckv1alpha1.Placement{{{PodName: "statefulset-name-0", VReplicas: 1}}},
			expected: State{Capacity: 10, FreeCap: []int32{int32(9)}, SchedulablePods: []int32{int32(0)}, LastOrdinal: 0, Replicas: 1, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-0": 1,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
				},
			},
			freec: int32(9),
		},
		{
			name:     "many vpods, no gaps",
			replicas: int32(3),
			vpods: [][]duckv1alpha1.Placement{
				{{PodName: "statefulset-name-0", VReplicas: 1}, {PodName: "statefulset-name-2", VReplicas: 5}},
				{{PodName: "statefulset-name-1", VReplicas: 2}},
				{{PodName: "statefulset-name-1", VReplicas: 3}, {PodName: "statefulset-name-0", VReplicas: 1}},
			},
			expected: State{Capacity: 10, FreeCap: []int32{int32(8), int32(5), int32(5)}, SchedulablePods: []int32{int32(0), int32(1), int32(2)}, LastOrdinal: 2, Replicas: 3, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-0": 1,
						"statefulset-name-2": 5,
					},
					{Name: vpodName + "-1", Namespace: vpodNs + "-1"}: {
						"statefulset-name-1": 2,
					},
					{Name: vpodName + "-2", Namespace: vpodNs + "-2"}: {
						"statefulset-name-0": 1,
						"statefulset-name-1": 3,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 0,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 0,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 1,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 1,
				},
			},
			freec: int32(18),
		},
		{
			name:            "many vpods, unschedulable pending pods (statefulset-name-0)",
			replicas:        int32(3),
			pendingReplicas: int32(1),
			vpods: [][]duckv1alpha1.Placement{
				{{PodName: "statefulset-name-0", VReplicas: 1}, {PodName: "statefulset-name-2", VReplicas: 5}},
				{{PodName: "statefulset-name-1", VReplicas: 2}},
				{{PodName: "statefulset-name-1", VReplicas: 3}, {PodName: "statefulset-name-0", VReplicas: 1}},
			},
			expected: State{Capacity: 10, FreeCap: []int32{int32(8), int32(5), int32(5)}, SchedulablePods: []int32{int32(1), int32(2)}, LastOrdinal: 2, Replicas: 3, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-2": 5,
					},
					{Name: vpodName + "-1", Namespace: vpodNs + "-1"}: {
						"statefulset-name-1": 2,
					},
					{Name: vpodName + "-2", Namespace: vpodNs + "-2"}: {
						"statefulset-name-1": 3,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 0,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 0,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 1,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 1,
				},
			},
			freec: int32(10),
		},
		{
			name:     "many vpods, with gaps",
			replicas: int32(4),
			vpods: [][]duckv1alpha1.Placement{
				{{PodName: "statefulset-name-0", VReplicas: 1}, {PodName: "statefulset-name-2", VReplicas: 5}},
				{{PodName: "statefulset-name-1", VReplicas: 0}},
				{{PodName: "statefulset-name-1", VReplicas: 0}, {PodName: "statefulset-name-3", VReplicas: 0}},
			},
			expected: State{Capacity: 10, FreeCap: []int32{int32(9), int32(10), int32(5), int32(10)}, SchedulablePods: []int32{int32(0), int32(1), int32(2), int32(3)}, LastOrdinal: 3, Replicas: 4, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-0": 1,
						"statefulset-name-2": 5,
					},
					{Name: vpodName + "-1", Namespace: vpodNs + "-1"}: {
						"statefulset-name-1": 0,
					},
					{Name: vpodName + "-2", Namespace: vpodNs + "-2"}: {
						"statefulset-name-1": 0,
						"statefulset-name-3": 0,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 1,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 1,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
					{Name: "vpod-name-1", Namespace: "vpod-ns-1"}: 1,
					{Name: "vpod-name-2", Namespace: "vpod-ns-2"}: 1,
				},
			},
			freec: int32(34),
		},
		{
			name:     "three vpods but one tainted and one with no zone label",
			replicas: int32(1),
			vpods:    [][]duckv1alpha1.Placement{{{PodName: "statefulset-name-0", VReplicas: 1}}},
			expected: State{Capacity: 10, FreeCap: []int32{int32(9)}, SchedulablePods: []int32{int32(0)}, LastOrdinal: 0, Replicas: 1, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-0": 1,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
				},
			},
			freec: int32(9),
		},
		{
			name:     "one vpod (HA)",
			replicas: int32(1),
			vpods:    [][]duckv1alpha1.Placement{{{PodName: "statefulset-name-0", VReplicas: 1}}},
			expected: State{Capacity: 10, FreeCap: []int32{int32(9)}, SchedulablePods: []int32{int32(0)}, LastOrdinal: 0, Replicas: 1, StatefulSetName: sfsName,
				PodSpread: map[types.NamespacedName]map[string]int32{
					{Name: vpodName + "-0", Namespace: vpodNs + "-0"}: {
						"statefulset-name-0": 1,
					},
				},
				Pending: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 0,
				},
				ExpectedVReplicaByVPod: map[types.NamespacedName]int32{
					{Name: "vpod-name-0", Namespace: "vpod-ns-0"}: 1,
				},
			},
			freec: int32(9),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := tscheduler.SetupFakeContext(t)
			vpodClient := tscheduler.NewVPodClient()
			podlist := make([]runtime.Object, 0, tc.replicas)

			if tc.pendingReplicas > tc.replicas {
				t.Fatalf("Inconsistent test configuration pending replicas %d greater than replicas %d", tc.pendingReplicas, tc.replicas)
			}

			for i, placements := range tc.vpods {
				vpodName := fmt.Sprint(vpodName+"-", i)
				vpodNamespace := fmt.Sprint(vpodNs+"-", i)

				vpodC := vpodClient.Create(vpodNamespace, vpodName, 1, placements)

				lsvp, err := vpodClient.List()
				if err != nil {
					t.Fatal("unexpected error", err)
				}
				vpodG := GetVPod(types.NamespacedName{Name: vpodName, Namespace: vpodNamespace}, lsvp)
				if !reflect.DeepEqual(vpodC, vpodG) {
					t.Errorf("unexpected vpod, got %v, want %v", vpodG, vpodC)
				}
			}

			for i := tc.replicas - 1; i >= 0; i-- {
				var pod *v1.Pod
				var err error
				if i < tc.pendingReplicas {
					podName := sfsName + "-" + fmt.Sprint(i)
					pod, err = kubeclient.Get(ctx).CoreV1().Pods(testNs).Create(ctx, tscheduler.MakePod(testNs, podName, ""), metav1.CreateOptions{})
				} else {
					nodeName := "node-" + fmt.Sprint(i)
					podName := sfsName + "-" + fmt.Sprint(i)
					pod, err = kubeclient.Get(ctx).CoreV1().Pods(testNs).Create(ctx, tscheduler.MakePod(testNs, podName, nodeName), metav1.CreateOptions{})
				}
				if err != nil {
					t.Fatal("unexpected error", err)
				}
				podlist = append(podlist, pod)
			}

			_, err := kubeclient.Get(ctx).AppsV1().StatefulSets(testNs).Create(ctx, tscheduler.MakeStatefulset(testNs, sfsName, tc.replicas), metav1.CreateOptions{})
			if err != nil {
				t.Fatal("unexpected error", err)
			}

			lsp := listers.NewListers(podlist)

			scaleCache := scheduler.NewScaleCache(ctx, testNs, kubeclient.Get(ctx).AppsV1().StatefulSets(testNs), scheduler.ScaleCacheConfig{RefreshPeriod: time.Minute * 5})

			stateBuilder := NewStateBuilder(sfsName, vpodClient.List, int32(10), lsp.GetPodLister().Pods(testNs), scaleCache)
			state, err := stateBuilder.State(ctx)
			if err != nil {
				t.Fatal("unexpected error", err)
			}

			tc.expected.PodLister = lsp.GetPodLister().Pods(testNs)
			if tc.expected.FreeCap == nil {
				tc.expected.FreeCap = make([]int32, 0, 256)
			}
			if tc.expected.PodSpread == nil {
				tc.expected.PodSpread = make(map[types.NamespacedName]map[string]int32)
			}
			if !reflect.DeepEqual(*state, tc.expected) {
				diff := cmp.Diff(tc.expected, *state, cmpopts.IgnoreInterfaces(struct{ corev1.PodNamespaceLister }{}))
				t.Errorf("unexpected state, got %v, want %v\n(-want, +got)\n%s", *state, tc.expected, diff)
			}

			if state.FreeCapacity() != tc.freec {
				t.Errorf("unexpected free capacity, got %d, want %d", state.FreeCapacity(), tc.freec)
			}
		})
	}
}
