package metering

// Billable state definitions per resource type.
// Only resources in these states produce metering entries.

var billableComputeInstanceStates = map[string]bool{
	"COMPUTE_INSTANCE_STATE_RUNNING": true,
}

var billableClusterStates = map[string]bool{
	"CLUSTER_STATE_READY":       true,
	"CLUSTER_STATE_PROGRESSING": true,
}

func IsComputeInstanceBillable(state string) bool {
	return billableComputeInstanceStates[state]
}

func IsClusterBillable(state string) bool {
	return billableClusterStates[state]
}

var billableModelStates = map[string]bool{
	"MODEL_STATE_RUNNING": true,
}

func IsModelBillable(state string) bool {
	return billableModelStates[state]
}

var billableBareMetalStates = map[string]bool{
	"BARE_METAL_INSTANCE_STATE_RUNNING": true,
}

func IsBareMetalBillable(state string) bool {
	return billableBareMetalStates[state]
}
