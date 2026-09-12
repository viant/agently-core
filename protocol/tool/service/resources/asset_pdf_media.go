package resources

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/ledongthuc/pdf"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

func validateImageInput(data []byte) error {
	c, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid image")
	}
	if c.Width <= 0 || c.Height <= 0 || int64(c.Width)*int64(c.Height) > 40_000_000 {
		return fmt.Errorf("image exceeds decoded pixel limit")
	}
	return nil
}

// Optional Poppler backends are advertised only when installed. Commands use
// fixed argument vectors and private input/output directories, never a shell.
func (s *Service) exportPDFMedia(ctx context.Context, a *asset, req *ExportInput, result *ExportOutput) error {
	if a.kind != "pdf" {
		return fmt.Errorf("PDF media export requires a PDF")
	}
	if req.Options != nil {
		return fmt.Errorf("PDF media export does not accept extraction options")
	}
	pages := []int{}
	if req.Select != nil {
		if req.Select.Sheet != "" || req.Select.Range != "" || req.Select.ComponentID != "" {
			return fmt.Errorf("select PDF pages")
		}
		pages = req.Select.Pages
	}
	reader, err := pdf.NewReader(bytes.NewReader(a.data), int64(len(a.data)))
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("select up to 16 PDF pages")
	}
	if len(pages) > 16 {
		return fmt.Errorf("PDF media page limit exceeded")
	}
	dpi := req.Output.DPI
	if dpi == 0 {
		dpi = 144
	}
	if dpi < 36 || dpi > 200 {
		return fmt.Errorf("DPI must be between 36 and 200")
	}
	program := "pdftoppm"
	if req.Operation == "extractImages" {
		program = "pdfimages"
	}
	binary, err := exec.LookPath(program)
	if err != nil {
		return fmt.Errorf("%s backend unavailable", program)
	}
	if req.Operation == "render" && req.Output.Format != "png" {
		return fmt.Errorf("PDF rendering supports png")
	}
	if req.Operation == "extractImages" && req.Output.Format != "original" && req.Output.Format != "png" {
		return fmt.Errorf("image extraction supports original or png")
	}
	dir, err := os.MkdirTemp("", "agently-pdf-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input.pdf")
	if err = os.WriteFile(input, a.data, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var outputFiles []string
	for index, page := range pages {
		if page < 1 || page > reader.NumPage() {
			return fmt.Errorf("invalid PDF page")
		}
		prefix := filepath.Join(dir, fmt.Sprintf("out-%d", index))
		args := []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page)}
		if req.Operation == "render" {
			args = append(args, "-singlefile", "-scale-to", "4096", "-r", strconv.Itoa(dpi), "-png", input, prefix)
		} else {
			flag := "-all"
			if req.Output.Format == "png" {
				flag = "-png"
			}
			args = append(args, flag, input, prefix)
		}
		if err = runPDFCommand(ctx, exec.CommandContext(ctx, binary, args...), dir); err != nil {
			return fmt.Errorf("PDF media conversion failed: %w", ctxOrError(ctx, err))
		}
		files, _ := filepath.Glob(prefix + "*")
		outputFiles = append(outputFiles, files...)
		if len(outputFiles) > 64 {
			return fmt.Errorf("PDF image count limit exceeded")
		}
	}
	sort.Strings(outputFiles)
	// Validate all output bounds before publishing any resource.
	total := int64(0)
	for _, f := range outputFiles {
		info, e := os.Stat(f)
		if e != nil {
			return e
		}
		total += info.Size()
		if total > scratchpadsvc.MaxArtifactBytes {
			return fmt.Errorf("PDF output byte limit exceeded")
		}
	}
	result.SourceVersion = a.version
	result.Resources = []*scratchpadsvc.ArtifactDescriptor{}
	for _, f := range outputFiles {
		data, e := os.ReadFile(f)
		if e != nil {
			return e
		}
		mime := "application/octet-stream"
		if _, format, e := image.DecodeConfig(bytes.NewReader(data)); e == nil {
			mime = "image/" + format
		}
		d, e := scratchpadsvc.New().PublishArtifact(ctx, "", filepath.Base(f), mime, a.uri, bytes.NewReader(data))
		if e != nil {
			return e
		}
		result.Resources = append(result.Resources, d)
	}
	result.Complete = true
	return nil
}
func ctxOrError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Kill conversion if output grows beyond the managed budget while it runs.
func runPDFCommand(ctx context.Context, cmd *exec.Cmd, dir string) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			return ctx.Err()
		case <-ticker.C:
			entries, err := os.ReadDir(dir)
			if err != nil {
				_ = cmd.Process.Kill()
				<-done
				return err
			}
			total := int64(0)
			for _, entry := range entries {
				if entry.Name() == "input.pdf" {
					continue
				}
				info, e := entry.Info()
				if e != nil {
					continue
				}
				total += info.Size()
			}
			if len(entries) > 66 || total > scratchpadsvc.MaxArtifactBytes {
				_ = cmd.Process.Kill()
				<-done
				return fmt.Errorf("PDF output limit exceeded")
			}
		}
	}
}
