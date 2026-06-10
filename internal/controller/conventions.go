package controller

import "time"

const (
	FinalizerName = "towonel-operator.towonel.io/finalizer"
	FieldOwner    = "towonel-operator"

	AnnotationInviteID = "towonel.io/invite-id"
	LabelPartOf        = "app.kubernetes.io/part-of"
	PartOfValue        = "towonel-operator"

	CondReady             = "Ready"
	CondInviteIssued      = "InviteIssued"
	CondHostnamesSynced   = "HostnamesSynced"
	CondTokenExpiringSoon = "TokenExpiringSoon"

	ReasonInvalidConfig = "InvalidConfig"
	ReasonReconciling   = "Reconciling"
	ReasonReady         = "Ready"
	ReasonAPIError      = "APIError"
	ReasonSynced        = "Synced"
	ReasonExpiringSoon  = "ExpiringSoon"
	ReasonNotExpiring   = "NotExpiring"

	renewWindow     = 7 * 24 * time.Hour
	defaultTokenKey = "token"
	tokenDataKey    = "token"
)

func tokenSecretName(tunnelName string) string { return tunnelName + "-token" }
