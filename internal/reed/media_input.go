package reed

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/mimetype"
)

// processMediaInputs uploads local files for media-typed inputs and replaces
// the raw path values with media:// URIs. Modifies req.Inputs in place.
func processMediaInputs(ctx context.Context, uploader MediaUploader, wf *model.Workflow, req *model.RunRequest) error {
	for id, spec := range wf.Inputs {
		switch spec.Type {
		case "media":
			v, ok := req.Inputs[id]
			if !ok {
				continue
			}
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			uploaded, err := uploadIfLocalPath(ctx, uploader, s)
			if err != nil {
				return fmt.Errorf("input %q: %w", id, err)
			}
			req.Inputs[id] = uploaded

		case "[]media":
			v, ok := req.Inputs[id]
			if !ok {
				continue
			}
			var paths []string
			switch val := v.(type) {
			case []string:
				for _, s := range val {
					for _, p := range strings.Split(s, ",") {
						p = strings.TrimSpace(p)
						if p != "" {
							paths = append(paths, p)
						}
					}
				}
			case string:
				for _, p := range strings.Split(val, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						paths = append(paths, p)
					}
				}
			default:
				continue
			}
			uploaded := make([]string, 0, len(paths))
			for _, p := range paths {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				u, err := uploadIfLocalPath(ctx, uploader, p)
				if err != nil {
					return fmt.Errorf("input %q: %w", id, err)
				}
				uploaded = append(uploaded, u)
			}
			req.Inputs[id] = uploaded
		}
	}
	return nil
}

// uploadIfLocalPath uploads a file if the value is a local path.
// Returns the value unchanged if it's already a media:// or data: URI.
func uploadIfLocalPath(ctx context.Context, uploader MediaUploader, value string) (string, error) {
	if media.IsMediaURI(value) || media.IsDataURI(value) {
		return value, nil
	}
	f, err := os.Open(value)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", value, err)
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n > 0 {
		f.Seek(0, 0)
	}
	mimeType := mimetype.Detect(value, buf[:n])
	entry, err := uploader.Upload(ctx, f, mimeType)
	if err != nil {
		return "", fmt.Errorf("upload %q: %w", value, err)
	}
	return media.URI(entry.ID), nil
}
