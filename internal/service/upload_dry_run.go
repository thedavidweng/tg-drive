package service

// UploadPlan is the dry-run preview of an upload. It touches neither the
// index nor Telegram, so it needs no App and no confirmation.
type UploadPlan struct {
	Local  []string       `json:"local"`
	Policy ConflictPolicy `json:"policy"`
	Remote string         `json:"remote"`
	// WouldReplace names the remote path a replace upload would overwrite.
	WouldReplace string `json:"would_replace,omitempty"`
}

// PlanUpload previews uploading locals to remote under policy.
func PlanUpload(locals []string, remote string, policy ConflictPolicy) UploadPlan {
	plan := UploadPlan{Local: locals, Remote: remote, Policy: policy}
	if policy == ConflictReplace {
		plan.WouldReplace = remote
	}
	return plan
}
