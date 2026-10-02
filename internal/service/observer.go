package service

// An Observer watches one long-running call (ADR 0032). It is a per-call
// option, so concurrent calls on one App report to their own observers. Every
// callback is optional. Callbacks may run on several goroutines at once and
// must return promptly; they cannot fail the call, which a caller stops by
// cancelling its context.
type Observer struct {
	// OnStage reports an item entering a stage.
	OnStage func(Item, Stage)
	// OnProgress reports an item's byte progress.
	OnProgress func(Progress)
	// OnItem reports an item's outcome, once per item the call accounts for.
	OnItem func(ItemResult)
}

// Stage is where one item of a call is in its lifecycle.
type Stage string

const (
	// StageHashing computes the item's content hash.
	StageHashing Stage = "hashing"
	// StageUploading sends the item's bytes to Telegram.
	StageUploading Stage = "uploading"
	// StagePublishing writes the item's machine records and index rows.
	StagePublishing Stage = "publishing"
)

// Item names one file a call works on.
type Item struct {
	// Source is the local file, when the item has one.
	Source string
	// Path is the item's canonical remote path.
	Path string
}

// Progress is an item's byte progress: Done of Total bytes.
type Progress struct {
	Item  Item
	Done  int64
	Total int64
	// Part is the confirmed upload part behind this report; nil when the
	// progress is not part-based.
	Part *UploadPart
}

// UploadPart is one confirmed part of an upload as the Telegram client saw
// it.
type UploadPart struct {
	// FileName is the name the upload carries on Telegram.
	FileName string
	Index    int
	Size     int
}

// ItemStatus is an item's outcome.
type ItemStatus string

const (
	ItemCompleted ItemStatus = "completed"
	ItemSkipped   ItemStatus = "skipped"
	ItemFailed    ItemStatus = "failed"
)

// ItemResult is one item's outcome. Err is set when the item failed.
type ItemResult struct {
	Item   Item
	Status ItemStatus
	Err    error
}

func (o Observer) stage(it Item, st Stage) {
	if o.OnStage != nil {
		o.OnStage(it, st)
	}
}

func (o Observer) progress(p Progress) {
	if o.OnProgress != nil {
		o.OnProgress(p)
	}
}

func (o Observer) item(r ItemResult) {
	if o.OnItem != nil {
		o.OnItem(r)
	}
}
