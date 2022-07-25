package types

type RefTypeMetadata string

const (
	HelmChartType RefTypeMetadata = "helm-chart"
	OciRefType    RefTypeMetadata = "oci-ref"
)

// ImageSpec defines OCI Image specifications
type ImageSpec struct {
	// Repo defines the Image repo
	Repo string `json:"repo"`

	// Name defines the Image name
	Name string `json:"name"`

	// Ref is either a sha value, tag or version
	Ref string `json:"ref"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref
	Type RefTypeMetadata `json:"type"`
}

// HelmChartSpec defines the specification for a helm chart
type HelmChartSpec struct {
	// Url defines the helm repo URL
	// +kubebuilder:validation:Optional
	Url string `json:"url"`

	// ChartName defines the helm chart name
	// +kubebuilder:validation:Optional
	ChartName string `json:"chartName"`

	// Type defines the chart as "oci-ref"
	// +kubebuilder:validation:Enum=helm-chart;oci-ref
	// +kubebuilder:validation:Optional
	Type RefTypeMetadata `json:"type"`
}
