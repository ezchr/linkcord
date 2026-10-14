package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gabstv/go-bsdiff/pkg/bspatch"
)

// errNoPatch means the release has no patch from the version running.
var errNoPatch = errors.New("no patch from this version")

// maxPatchSize bounds a patch; past it the whole file is as cheap.
const maxPatchSize = 64 << 20

// PatchName is the release asset that turns version from's executable into
// the release's: a bsdiff patch, made with cmd/linkcord-patch.
func PatchName(from string) string {
	return "linkcord-" + from + ".patch"
}

// fetchPatch downloads the patch from the version running, applies it to the
// executable at target and writes the result to staged, only if it is the
// release's exact executable.
func (m *Manager) fetchPatch(ctx context.Context, release *Release, target string, full Asset, expected []byte, staged string, progress func(int)) error {
	var patch *Asset
	for i, a := range release.Assets {
		if a.Name == PatchName(m.Version) {
			patch = &release.Assets[i]
		}
	}
	if patch == nil {
		return errNoPatch
	}
	if patch.ID <= 0 || patch.Size <= 0 || patch.Size > maxPatchSize || patch.Size >= full.Size {
		return errors.New("invalid patch asset")
	}
	old, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	counter := &progressWriter{size: patch.Size, last: -1, progress: progress}
	n, err := m.download(ctx, *patch, &multi{&buf, counter})
	if err != nil {
		return err
	}
	if n != patch.Size {
		return errors.New("patch download was cut short")
	}
	// The patch's own digest, when GitHub gives one, guards the download.
	if d, ok := strings.CutPrefix(patch.Digest, "sha256:"); ok {
		sum := sha256.Sum256(buf.Bytes())
		if !strings.EqualFold(fmt.Sprintf("%x", sum), d) {
			return errors.New("patch SHA-256 verification failed")
		}
	}
	out, err := bspatch.Bytes(old, buf.Bytes())
	if err != nil {
		return fmt.Errorf("could not apply the patch: %w", err)
	}
	sum := sha256.Sum256(out)
	if int64(len(out)) != full.Size || !bytes.Equal(sum[:], expected) {
		// The running executable is not the release the patch was made from.
		return errors.New("patched executable does not match the release")
	}
	return os.WriteFile(staged, out, 0o700)
}

type multi [2]interface{ Write([]byte) (int, error) }

func (w *multi) Write(p []byte) (int, error) {
	for _, x := range w {
		if _, err := x.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
