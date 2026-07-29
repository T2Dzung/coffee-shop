package inventory

// External capability RBAC is kept separate from collector runtime behavior so a
// backward-compatible controller image can be rolled out before the CRD/RBAC/config
// cutover that enables these targets.
//
// +kubebuilder:rbac:groups=external-secrets.io,resources=externalsecrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates;certificaterequests,verbs=get;list;watch
