package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// observed records everything an Observer receives, one line per callback,
// so a test can compare a call's report against a worked example.
type observed struct {
	mu    sync.Mutex
	lines []string
}

func (o *observed) observer() Observer {
	return Observer{
		OnStage: func(it Item, st Stage) {
			o.add(fmt.Sprintf("stage %s %s", it.Path, st))
		},
		OnProgress: func(p Progress) {
			part := ""
			if p.Part != nil {
				part = fmt.Sprintf(" part=%s#%d/%d", p.Part.FileName, p.Part.Index, p.Part.Size)
			}
			o.add(fmt.Sprintf("progress %s %d/%d%s", p.Item.Path, p.Done, p.Total, part))
		},
		OnItem: func(r ItemResult) {
			line := fmt.Sprintf("item %s %s", r.Item.Path, r.Status)
			if r.Err != nil {
				line += " err"
			}
			o.add(line)
		},
	}
}

func (o *observed) add(line string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, line)
}

func (o *observed) all() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.lines)
}

func writeSized(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A single-file upload reports its stages in order, byte progress for every
// confirmed part at the per-call part size, and one completed item.
func TestUploadObserverSingleFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	tg.SetResumableThreshold(1024)
	ctx := context.Background()

	local := writeSized(t, t.TempDir(), "a.bin", 2500)
	var got observed
	if _, err := app.UploadFileAs(ctx, local, "/obs/a.bin", ConflictFail, false, Presentation{},
		UploadOptions{PartSizeKB: 1, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage /obs/a.bin hashing",
		"stage /obs/a.bin uploading",
		"progress /obs/a.bin 1024/2500 part=a.bin#0/1024",
		"progress /obs/a.bin 2048/2500 part=a.bin#1/1024",
		"progress /obs/a.bin 2500/2500 part=a.bin#2/1024",
		"stage /obs/a.bin publishing",
		"item /obs/a.bin completed",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// A multi-file upload reports every member: the one --skip-existing drops
// as skipped, the rest through their stages to completed.
func TestUploadObserverMultiFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	dir := t.TempDir()
	a := writeSized(t, dir, "a.bin", 10)
	b := writeSized(t, dir, "b.bin", 20)
	c := writeSized(t, dir, "c.bin", 30)
	if _, err := app.UploadFile(ctx, b, "/multi/b.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	var got observed
	if _, err := app.UploadFilesAs(ctx, []string{a, b, c}, "/multi/", ConflictSkip, false, Presentation{},
		UploadOptions{Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"item /multi/b.bin skipped",
		"stage /multi/a.bin hashing",
		"stage /multi/c.bin hashing",
		"stage /multi/a.bin uploading",
		"stage /multi/c.bin uploading",
		"stage /multi/a.bin publishing",
		"stage /multi/c.bin publishing",
		"item /multi/a.bin completed",
		"item /multi/c.bin completed",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// A recursive upload with --continue-on-error reports each file once: a
// skipped file, a file that fails while staging, and the rest completed.
func TestUploadObserverRecursive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	tree := t.TempDir()
	writeSized(t, tree, "a.bin", 10)
	unreadable := writeSized(t, tree, "b.bin", 20)
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := writeSized(t, filepath.Join(tree, "sub"), "c.bin", 30)
	if _, err := app.UploadFile(ctx, c, "/rec/sub/c.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	var got observed
	res, err := app.UploadRecursive(ctx, tree, "/rec", ConflictSkip, true, false, false,
		UploadOptions{Observer: got.observer()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploaded != 1 || res.Skipped != 1 || res.Failed != 1 {
		t.Fatalf("result = %+v, want 1 uploaded, 1 skipped, 1 failed", res)
	}
	want := []string{
		"stage /rec/a.bin hashing",
		"stage /rec/b.bin hashing",
		"item /rec/b.bin failed err",
		"stage /rec/a.bin uploading",
		"stage /rec/a.bin publishing",
		"item /rec/a.bin completed",
		"item /rec/sub/c.bin skipped",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// uploadRecorder records the transfer settings each upload request carried.
type uploadRecorder struct {
	telegram.Client
	mu   sync.Mutex
	reqs map[string]telegram.UploadRequest
}

func (r *uploadRecorder) UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error) {
	r.mu.Lock()
	r.reqs[req.FileName] = req
	r.mu.Unlock()
	return r.Client.UploadMedia(ctx, req)
}

// Concurrent calls on one App keep their own channel, transfer settings,
// and observer: nothing one call chooses leaks into another or into the App.
func TestUploadPerCallStateIsolated(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.InitRoot(ctx, t.TempDir(), "", "Second", ""); err != nil {
		t.Fatal(err)
	}
	tg.SetResumableThreshold(1024)
	rec := &uploadRecorder{Client: app.TG, reqs: map[string]telegram.UploadRequest{}}
	app.TG = rec
	cfgBefore := app.Cfg

	calls := []struct {
		channel string
		name    string
		opts    UploadOptions
	}{
		{channel: "Test Channel", name: "one.bin", opts: UploadOptions{Threads: 3, PartSizeKB: 1}},
		{channel: "Second", name: "two.bin", opts: UploadOptions{Threads: 5, PartSizeKB: 2}},
	}
	seen := make([]observed, len(calls))
	var wg sync.WaitGroup
	errs := make([]error, len(calls))
	for i, c := range calls {
		local := writeSized(t, t.TempDir(), c.name, 4096)
		opts := c.opts
		opts.Observer = seen[i].observer()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = app.UploadFileAs(WithChannel(ctx, c.channel), local, "/"+c.name, ConflictFail, false, Presentation{}, opts)
		}()
	}
	wg.Wait()

	for i, c := range calls {
		if errs[i] != nil {
			t.Fatalf("%s: %v", c.name, errs[i])
		}
		partSize := c.opts.PartSizeKB * 1024
		var want []string
		want = append(want, "stage /"+c.name+" hashing", "stage /"+c.name+" uploading")
		for done := partSize; done-partSize < 4096; done += partSize {
			want = append(want, fmt.Sprintf("progress /%s %d/4096 part=%s#%d/%d", c.name, min(done, 4096), c.name, done/partSize-1, partSize))
		}
		want = append(want, "stage /"+c.name+" publishing", "item /"+c.name+" completed")
		if !slices.Equal(seen[i].all(), want) {
			t.Fatalf("%s observed:\n%v\nwant:\n%v", c.name, seen[i].all(), want)
		}
		req := rec.reqs[c.name]
		if req.Threads != c.opts.Threads || req.PartSize != partSize {
			t.Fatalf("%s sent threads=%d part=%d, want %d and %d", c.name, req.Threads, req.PartSize, c.opts.Threads, partSize)
		}
		entries, err := app.ListDir(WithChannel(ctx, c.channel), "/")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name != c.name {
			t.Fatalf("channel %s lists %+v, want only %s", c.channel, entries, c.name)
		}
	}
	if app.Channel != "" || !reflect.DeepEqual(app.Cfg, cfgBefore) {
		t.Fatalf("calls changed the App: channel=%q cfg=%+v", app.Channel, app.Cfg)
	}
}

// A strict multi-file upload that aborts still accounts for every member:
// the one that failed and the ones the abort left unsent.
func TestUploadObserverAbortReportsEveryMember(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	dir := t.TempDir()
	a := writeSized(t, dir, "a.bin", 10)
	b := writeSized(t, dir, "b.bin", 20)
	c := writeSized(t, dir, "c.bin", 30)
	if err := os.Chmod(b, 0); err != nil {
		t.Fatal(err)
	}
	var got observed
	if _, err := app.UploadFilesAs(ctx, []string{a, b, c}, "/abort/", ConflictFail, false, Presentation{},
		UploadOptions{Observer: got.observer()}); err == nil {
		t.Fatal("expected the unreadable member to abort the upload")
	}
	want := []string{
		"stage /abort/a.bin hashing",
		"stage /abort/b.bin hashing",
		"item /abort/b.bin failed err",
		"item /abort/a.bin failed err",
		"item /abort/c.bin failed err",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}
