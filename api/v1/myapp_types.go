/*
Copyright 2026.
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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AutoscalingSpec defines the autoscaling configuration for MyApp
type AutoscalingSpec struct {
	// enabled controls whether autoscaling is enabled
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// minReplicas is the minimum number of replicas
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// maxReplicas is the maximum number of replicas
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// targetCPUUtilizationPercentage is the target CPU utilization percentage
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetCPUUtilizationPercentage *int32 `json:"targetCPUUtilizationPercentage,omitempty"`
}

// MyAppSpec defines the desired state of MyApp
type MyAppSpec struct {
	// replicas is the desired number of pod replicas
	// +kubebuilder:validation:Minimum=1
	// +required
	Replicas int32 `json:"replicas"`

	// port is the port that the application listens on
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +required
	Port int32 `json:"port"`

	// image is the container image to use
	// +kubebuilder:default="nginx:latest"
	// +optional
	Image string `json:"image,omitempty"`

	// config is a map of environment variables to inject
	// +optional
	Config map[string]string `json:"config,omitempty"`

	// autoscaling defines the autoscaling configuration
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`
}

// MyAppStatus defines the observed state of MyApp.
type MyAppStatus struct {
	// phase represents the current lifecycle phase of the MyApp
	// +optional
	Phase string `json:"phase,omitempty"`

	// readyReplicas is the number of pods that are ready
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// conditions represent the current state of the MyApp resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Desired",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Port",type="integer",JSONPath=".spec.port"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MyApp is the Schema for the myapps API
type MyApp struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
	// +required
	Spec MyAppSpec `json:"spec"`
	// +optional
	Status MyAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MyAppList contains a list of MyApp
type MyAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MyApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &MyApp{}, &MyAppList{})
		return nil
	})
}
