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
