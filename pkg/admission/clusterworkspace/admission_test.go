/*
Copyright 2022 The KCP Authors.

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

package clusterworkspace

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kcp-dev/logicalcluster"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"

	"github.com/kcp-dev/kcp/pkg/admission/helpers"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
)

func createAttr(ws *tenancyv1alpha1.ClusterWorkspace) admission.Attributes {
	return admission.NewAttributesRecord(
		helpers.ToUnstructuredOrDie(ws),
		nil,
		tenancyv1alpha1.Kind("ClusterWorkspace").WithVersion("v1alpha1"),
		"",
		ws.Name,
		tenancyv1alpha1.Resource("clusterworkspaces").WithVersion("v1alpha1"),
		"",
		admission.Create,
		&metav1.CreateOptions{},
		false,
		&user.DefaultInfo{},
	)
}

func updateAttr(ws, old *tenancyv1alpha1.ClusterWorkspace) admission.Attributes {
	return admission.NewAttributesRecord(
		helpers.ToUnstructuredOrDie(ws),
		helpers.ToUnstructuredOrDie(old),
		tenancyv1alpha1.Kind("ClusterWorkspace").WithVersion("v1alpha1"),
		"",
		ws.Name,
		tenancyv1alpha1.Resource("clusterworkspaces").WithVersion("v1alpha1"),
		"",
		admission.Update,
		&metav1.CreateOptions{},
		false,
		&user.DefaultInfo{},
	)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		a       admission.Attributes
		wantErr bool
	}{
		{
			name: "rejects type mutations",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Universal",
					},
				}),
			wantErr: true,
		},
		{
			name: "rejects unsetting location",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Location: tenancyv1alpha1.ClusterWorkspaceLocation{
						Current: "",
					},
				}},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Location: tenancyv1alpha1.ClusterWorkspaceLocation{
							Current: "cluster",
						},
					},
				}),
			wantErr: true,
		},
		{
			name: "rejects unsetting baseURL",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						BaseURL: "https://cluster/clsuters/test",
					},
				}),
			wantErr: true,
		},
		{
			name: "rejects transition from Initializing with non-empty initializers",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{"a"},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
					BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Phase:        tenancyv1alpha1.ClusterWorkspacePhaseInitializing,
						Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{"a"},
					},
				}),
			wantErr: true,
		},
		{
			name: "allows transition from Initializing with empty initializers",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
					BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Phase:        tenancyv1alpha1.ClusterWorkspacePhaseInitializing,
						Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{"a"},
						Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
						BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
					},
				}),
		},
		{
			name: "allows transition to ready directly when valid",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
					BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Phase:        tenancyv1alpha1.ClusterWorkspacePhaseScheduling,
						Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{"a"},
					},
				}),
		},
		{
			name: "allows creation to ready directly when valid",
			a: createAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
					BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
				},
			}),
		},
		{
			name: "rejects transition to ready directly when invalid",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Phase:        tenancyv1alpha1.ClusterWorkspacePhaseScheduling,
						Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{"a"},
					},
				}),
			wantErr: true,
		},
		{
			name: "rejects transition to previous phase",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
					Type: "Foo",
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase:        tenancyv1alpha1.ClusterWorkspacePhaseInitializing,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
					Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
					BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
				},
			},
				&tenancyv1alpha1.ClusterWorkspace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
						Type: "Foo",
					},
					Status: tenancyv1alpha1.ClusterWorkspaceStatus{
						Phase:        tenancyv1alpha1.ClusterWorkspacePhaseReady,
						Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{},
						Location:     tenancyv1alpha1.ClusterWorkspaceLocation{Current: "somewhere"},
						BaseURL:      "https://kcp.bigcorp.com/clusters/org:test",
					},
				}),
			wantErr: true,
		},
		{
			name: "ignores different resources",
			a: admission.NewAttributesRecord(
				&tenancyv1alpha1.ClusterWorkspaceShard{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
				},
				nil,
				tenancyv1alpha1.Kind("ClusterWorkspaceShard").WithVersion("v1alpha1"),
				"",
				"test",
				tenancyv1alpha1.Resource("clusterworkspaceshards").WithVersion("v1alpha1"),
				"",
				admission.Create,
				&metav1.CreateOptions{},
				false,
				&user.DefaultInfo{},
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &clusterWorkspace{
				Handler: admission.NewHandler(admission.Create, admission.Update),
			}
			ctx := request.WithCluster(context.Background(), request.Cluster{Name: logicalcluster.New("root:org")})
			if err := o.Validate(ctx, tt.a, nil); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAdmit(t *testing.T) {
	tests := []struct {
		name           string
		a              admission.Attributes
		expectedLabels map[string]string
		wantErr        bool
	}{
		{
			name: "adds missing labels on creation",
			a: createAttr(&tenancyv1alpha1.ClusterWorkspace{
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase: tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"pluto", "venus", "apollo",
					},
				},
			}),
			expectedLabels: map[string]string{
				"internal.kcp.dev/phase":              "Ready",
				"internal.kcp.dev/initializer.pluto":  "",
				"internal.kcp.dev/initializer.venus":  "",
				"internal.kcp.dev/initializer.apollo": "",
			},
		},
		{
			name: "adds missing labels removed on mutation",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"internal.kcp.dev/phase":              "Ready",
						"internal.kcp.dev/initializer.pluto":  "",
						"internal.kcp.dev/initializer.venus":  "",
						"internal.kcp.dev/initializer.apollo": "",
					},
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase: tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"pluto", "venus", "apollo",
					},
				},
			}, &tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"internal.kcp.dev/initializer.pluto":  "",
						"internal.kcp.dev/initializer.apollo": "",
					},
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase: tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"pluto", "venus", "apollo",
					},
				},
			}),
			expectedLabels: map[string]string{
				"internal.kcp.dev/phase":              "Ready",
				"internal.kcp.dev/initializer.pluto":  "",
				"internal.kcp.dev/initializer.venus":  "",
				"internal.kcp.dev/initializer.apollo": "",
			},
		},
		{
			name: "removes previously-needed labels removed on mutation that removes initializer",
			a: updateAttr(&tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"internal.kcp.dev/phase":             "Ready",
						"internal.kcp.dev/initializer.pluto": "",
						"internal.kcp.dev/initializer.venus": "",
					},
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase: tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"pluto",
					},
				},
			}, &tenancyv1alpha1.ClusterWorkspace{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"internal.kcp.dev/phase":             "Ready",
						"internal.kcp.dev/initializer.pluto": "",
						"internal.kcp.dev/initializer.venus": "",
					},
				},
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Phase: tenancyv1alpha1.ClusterWorkspacePhaseReady,
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"pluto", "venus",
					},
				},
			}),
			expectedLabels: map[string]string{
				"internal.kcp.dev/phase":             "Ready",
				"internal.kcp.dev/initializer.pluto": "",
			},
		},
		{
			name: "error from adding an initializer that's too long",
			a: createAttr(&tenancyv1alpha1.ClusterWorkspace{
				Status: tenancyv1alpha1.ClusterWorkspaceStatus{
					Initializers: []tenancyv1alpha1.ClusterWorkspaceInitializer{
						"really-could-be-a-shorter-name-but-we-didnt-know-about-the-length-restriction",
					},
				},
			}),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &clusterWorkspace{
				Handler: admission.NewHandler(admission.Create, admission.Update),
			}
			ctx := request.WithCluster(context.Background(), request.Cluster{Name: logicalcluster.New("root:org")})
			if err := o.Admit(ctx, tt.a, nil); (err != nil) != tt.wantErr {
				t.Errorf("Admit() error = %v, wantErr %v", err, tt.wantErr)
			}

			u, ok := tt.a.GetObject().(*unstructured.Unstructured)
			require.True(t, ok, "unexpected type %T", tt.a.GetObject())

			cw := &tenancyv1alpha1.ClusterWorkspace{}
			require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, cw),
				"failed to convert unstructured to ClusterWorkspace",
			)

			require.Empty(t, cmp.Diff(cw.ObjectMeta.Labels, tt.expectedLabels))
		})
	}
}
