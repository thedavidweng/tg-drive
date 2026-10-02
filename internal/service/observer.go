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
	// StageDownloading receives the item's bytes from Telegram.
	StageDownloading Stage = "downloading"
	// StageReading reads Telegram history for the call as a whole.
	StageReading Stage = "reading"
	// StageIndexing rebuilds the local index from the history read, for the
	// call as a whole.
	StageIndexing Stage = "indexing"
)

// Item names one file or message a call works on. The zero Item stands for
// the call as a whole, which some stages describe.
type Item struct {
	// Source is the local file the item reads from or writes to, when the
	// item has one.
	Source string
	// Path is the item's canonical remote path, when it has one.
	Path string
	// MessageID is the Telegram message the item is, when the call works on
	// messages rather than local files.
	MessageID int
}

// Progress is an item's byte progress: Done of Total bytes.
type Progress struct {
	Item Item
	Done int64
	// Total is 0 when the size is not known in advance.
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

// progressWriter reports every byte written through it as item progress. It
// writes nowhere; tee it after the real destination so only bytes the
// destination accepted are counted.
type progressWriter struct {
	obs   Observer
	item  Item
	done  int64
	total int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	w.obs.progress(Progress{Item: w.item, Done: w.done, Total: w.total})
	return len(p), nil
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

// changes is the observer for reports of the work a call does: o, or none
// for a dry run, which does no work and reports only what it reads.
func (o Observer) changes(dryRun bool) Observer {
	if dryRun {
		return Observer{}
	}
	return o
}

// stages is o without its per-item results, for a call that runs another
// use case for an item and reports that item's result itself.
func (o Observer) stages() Observer {
	o.OnItem = nil
	return o
}

// outcome reports it by the action a call's result records for it: "skip"
// as skipped, "fail" as failed with err, any other action as completed.
func (o Observer) outcome(it Item, action string, err error) {
	switch action {
	case "skip":
		o.item(ItemResult{Item: it, Status: ItemSkipped})
	case "fail":
		o.item(ItemResult{Item: it, Status: ItemFailed, Err: err})
	default:
		o.item(ItemResult{Item: it, Status: ItemCompleted})
	}
}

// done reports it completed when err is nil and failed otherwise.
func (o Observer) done(it Item, err error) {
	if err != nil {
		o.item(ItemResult{Item: it, Status: ItemFailed, Err: err})
		return
	}
	o.item(ItemResult{Item: it, Status: ItemCompleted})
}
