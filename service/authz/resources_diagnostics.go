package authz

var (
	DiagnosticsRead   = Permission{Resource: "model_diagnostics", Action: "read"}
	DiagnosticsCreate = Permission{Resource: "model_diagnostics", Action: "create"}
)

func init() {
	RegisterResource(ResourceDefinition{Resource: "model_diagnostics", LabelKey: "Model diagnostics", Actions: []ActionDefinition{
		{Action: "read", LabelKey: "View diagnostics", DefaultRoles: []string{BuiltInRoleUser, BuiltInRoleAdmin}},
		{Action: "create", LabelKey: "Start diagnostics", DefaultRoles: []string{BuiltInRoleUser, BuiltInRoleAdmin}},
	}})
}
