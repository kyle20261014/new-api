package authz

const ResourceRouting = "routing"

var RoutingReconcile = Permission{Resource: ResourceRouting, Action: "reconcile"}

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceRouting,
		LabelKey: "Smart routing",
		Actions: []ActionDefinition{{
			Action: "reconcile", LabelKey: "Reconcile uncertain submissions",
			DescriptionKey: "Record verified submission outcomes and recover reserved funds without creating another upstream task.",
		}},
	})
}
