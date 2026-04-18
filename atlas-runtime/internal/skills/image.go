package skills

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/creds"

	"github.com/google/uuid"
)

// Default image model IDs — used when the user has not selected a specific model.
// gpt-image-1.5 is the current recommended OpenAI image generation and editing model
// (as of May 2026; replaces deprecated DALL-E series retired May 12, 2026).
// gemini-2.5-flash-image is the current stable Gemini native image model.
const (
	openAIImageModel = "gpt-image-1.5"
	geminiImageModel = "gemini-2.5-flash-image"
)

// ImageUsageHook is called after a successful image generation to record usage.
// Wire this in chat/service.go NewService() to persist to the database.
var ImageUsageHook func(provider, model, quality string, count int)

func (r *Registry) registerImage() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "image.generate",
			Description: "Generate an image from a text prompt. Uses the active cloud AI provider (OpenAI or Gemini). Returns a markdown image rendered inline in chat.",
			Properties: map[string]ToolParam{
				"prompt": {Description: "A description of the image to generate", Type: "string"},
				"size": {
					Description: "Image size — OpenAI: 1024x1024, 1792x1024, 1024x1792 | Gemini: 1:1, 16:9, 9:16, 4:3, 3:4 (default 1:1)",
					Type:        "string",
				},
				"quality": {
					Description: "Image quality for OpenAI: auto (default), low, medium, or high. Ignored for Gemini.",
					Type:        "string",
					Enum:        []string{"auto", "low", "medium", "high"},
				},
				"n": {Description: "Number of images to generate (1–4, default 1). Gemini supports 1 only.", Type: "integer"},
			},
			Required: []string{"prompt"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		FnResult:    imageGenerate,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "image.edit",
			Description: "Edit an existing image using a text instruction. Uses the active cloud AI provider (OpenAI or Gemini). Accepts PNG, JPEG, or WebP files up to 50MB.",
			Properties: map[string]ToolParam{
				"imagePath": {Description: "Absolute path to the source image file (PNG, JPEG, or WebP)", Type: "string"},
				"prompt":    {Description: "Editing instruction describing the change to make", Type: "string"},
				"size": {
					Description: "Output image size — OpenAI: 1024x1024, 1536x1024, 1024x1536, auto (default). Gemini aspect ratio: 1:1 (default), 16:9, 9:16, 4:3, 3:4.",
					Type:        "string",
				},
				"n": {Description: "Number of edited images to generate (1–10, default 1). Gemini supports 1 only.", Type: "integer"},
			},
			Required: []string{"imagePath", "prompt"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassExternalSideEffect,
		FnResult:    imageEdit,
	})
}

func imageGenerate(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Prompt  string `json:"prompt"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
		N       int    `json:"n"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Prompt == "" {
		return ToolResult{}, fmt.Errorf("prompt is required")
	}
	if p.N <= 0 {
		p.N = 1
	}
	if p.N > 4 {
		p.N = 4
	}

	cfg := config.NewStore().Load()

	var markdown string
	var absPaths []string

	switch cfg.ActiveAIProvider {
	case "openai":
		model := cfg.SelectedOpenAIImageModel
		if model == "" {
			model = openAIImageModel
		}
		quality := p.Quality
		if quality == "" {
			quality = "auto"
		}
		var err error
		markdown, absPaths, err = imageGenerateOpenAI(p.Prompt, p.Size, quality, p.N, model)
		if err != nil {
			return ToolResult{}, err
		}
		if ImageUsageHook != nil {
			ImageUsageHook("openai", model, quality, p.N)
		}
	case "gemini":
		model := cfg.SelectedGeminiImageModel
		if model == "" {
			model = geminiImageModel
		}
		var err error
		markdown, absPaths, err = imageGenerateGemini(p.Prompt, p.Size, model)
		if err != nil {
			return ToolResult{}, err
		}
		if ImageUsageHook != nil {
			ImageUsageHook("gemini", model, "auto", p.N)
		}
	default:
		return ToolResult{}, fmt.Errorf(
			"image generation is not supported for provider %q — switch to OpenAI or Gemini in Settings → AI Provider",
			cfg.ActiveAIProvider,
		)
	}

	artifacts := map[string]any{}
	for i, p := range absPaths {
		if i == 0 {
			artifacts["_image_path"] = p
		} else {
			artifacts[fmt.Sprintf("_image_path_%d", i+1)] = p
		}
	}
	_ = markdown // image is surfaced via file_generated; tell the AI to describe what it created
	return ToolResult{
		Success:   true,
		Summary:   fmt.Sprintf("Image generated successfully (%d image(s)). Briefly describe what you created for the user.", len(absPaths)),
		Artifacts: artifacts,
	}, nil
}

func imageEdit(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		ImagePath string `json:"imagePath"`
		Prompt    string `json:"prompt"`
		Size      string `json:"size"`
		N         int    `json:"n"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ImagePath == "" || p.Prompt == "" {
		return ToolResult{}, fmt.Errorf("imagePath and prompt are required")
	}
	if p.N <= 0 {
		p.N = 1
	}
	if p.N > 10 {
		p.N = 10
	}

	cfg := config.NewStore().Load()

	// Prepare the image once: decode, center-crop to square, scale to 1024×1024, encode as PNG.
	// This normalises the input for all providers and keeps payloads small.
	srcAbs, err := filepath.Abs(p.ImagePath)
	if err != nil {
		return ToolResult{}, fmt.Errorf("invalid image path: %w", err)
	}
	prepared, err := prepareImageForEdit(srcAbs)
	if err != nil {
		return ToolResult{}, fmt.Errorf("image prepare failed: %w", err)
	}

	switch cfg.ActiveAIProvider {
	case "gemini":
		model := cfg.SelectedGeminiImageModel
		if model == "" {
			model = geminiImageModel
		}
		md, absPath, err := imageEditGemini(prepared, p.Prompt, p.Size, model)
		if err != nil {
			return ToolResult{}, err
		}
		_ = md
		if ImageUsageHook != nil {
			ImageUsageHook("gemini", model, "auto", 1)
		}
		return ToolResult{
			Success:   true,
			Summary:   "Image edited successfully. Briefly describe what was changed for the user.",
			Artifacts: map[string]any{"_image_path": absPath},
		}, nil
	case "openai":
		// fall through to OpenAI path below
	default:
		return ToolResult{}, fmt.Errorf(
			"image editing is not supported for provider %q — switch to OpenAI or Gemini in Settings → AI Provider",
			cfg.ActiveAIProvider,
		)
	}

	bundle, _ := creds.Read()
	if bundle.OpenAIAPIKey == "" {
		return ToolResult{}, fmt.Errorf("image editing requires an OpenAI API key — add it in Settings → Credentials")
	}

	// Use the configured image model; fall back to dall-e-2 if the API rejects it.
	// /v1/images/edits is documented to support gpt-image-* but currently rejects
	// them with "Value must be 'dall-e-2'" (OpenAI rollout bug, tracked at
	// https://community.openai.com/t/edit-endpoint-images-edits-refusing-gpt-image-models/1375581).
	// When fixed, the configured model succeeds on the first attempt automatically.
	preferredModel := cfg.SelectedOpenAIImageModel
	if preferredModel == "" {
		preferredModel = openAIImageModel
	}

	doEdit := func(model string) ([]byte, int, error) {
		size := p.Size
		if size == "" {
			if strings.HasPrefix(model, "dall-e") {
				size = "1024x1024"
			} else {
				size = "auto"
			}
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		imgPart, err := w.CreateFormFile("image", "image.png")
		if err != nil {
			return nil, 0, err
		}
		if _, err := imgPart.Write(prepared); err != nil {
			return nil, 0, err
		}
		w.WriteField("prompt", p.Prompt)           //nolint:errcheck
		w.WriteField("model", model)               //nolint:errcheck
		w.WriteField("n", fmt.Sprintf("%d", p.N)) //nolint:errcheck
		if size != "auto" {
			w.WriteField("size", size) //nolint:errcheck
		}
		if strings.HasPrefix(model, "dall-e") {
			w.WriteField("response_format", "b64_json") //nolint:errcheck
		}
		w.Close()
		req, err := http.NewRequest("POST", "https://api.openai.com/v1/images/edits", &body)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+bundle.OpenAIAPIKey)
		resp, err := newWebClient(90 * time.Second).Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
		return respBody, resp.StatusCode, nil
	}

	respBody, statusCode, err := doEdit(preferredModel)
	if err != nil {
		return ToolResult{}, err
	}

	// If the API rejects the preferred model with the known rollout error, retry with dall-e-2.
	if statusCode != 200 {
		var errResp struct {
			Error struct{ Message string `json:"message"` } `json:"error"`
		}
		json.Unmarshal(respBody, &errResp) //nolint:errcheck
		if strings.Contains(errResp.Error.Message, "dall-e-2") && preferredModel != "dall-e-2" {
			respBody, statusCode, err = doEdit("dall-e-2")
			if err != nil {
				return ToolResult{}, err
			}
		}
	}

	if statusCode != 200 {
		var errResp struct {
			Error struct{ Message string `json:"message"` } `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return ToolResult{}, fmt.Errorf("OpenAI image edit: %s", errResp.Error.Message)
		}
		return ToolResult{}, fmt.Errorf("OpenAI image edit HTTP %d: %s", statusCode, string(respBody))
	}

	var result struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || len(result.Data) == 0 {
		return ToolResult{Success: true, Summary: "No edited images returned."}, nil
	}

	artifacts := map[string]any{}
	for i, img := range result.Data {
		_, imgAbsPath, saveErr := saveImageData(img.B64JSON, img.URL)
		if saveErr != nil {
			return ToolResult{}, fmt.Errorf("failed to save edited image: %w", saveErr)
		}
		if i == 0 {
			artifacts["_image_path"] = imgAbsPath
		} else {
			artifacts[fmt.Sprintf("_image_path_%d", i+1)] = imgAbsPath
		}
	}
	if ImageUsageHook != nil {
		ImageUsageHook("openai", preferredModel, "auto", len(result.Data))
	}
	return ToolResult{
		Success:   true,
		Summary:   "Image edited successfully. Briefly describe what was changed for the user.",
		Artifacts: artifacts,
	}, nil
}

// imageGenerateOpenAI calls the OpenAI Images API.
// gpt-image-1 and newer always return b64_json — response_format param is not supported.
// Returns (markdownResult, absFilePaths, error).
func imageGenerateOpenAI(prompt, size, quality string, n int, model string) (string, []string, error) {
	bundle, _ := creds.Read()
	if bundle.OpenAIAPIKey == "" {
		return "", nil, fmt.Errorf("OpenAI API key not configured — add it in Settings → Credentials")
	}
	if size == "" {
		size = "1024x1024"
	}
	if quality == "" {
		quality = "auto"
	}

	payload := map[string]any{
		"model":   model,
		"prompt":  prompt,
		"n":       n,
		"size":    size,
		"quality": quality,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bundle.OpenAIAPIKey)

	resp, err := newWebClient(90 * time.Second).Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct{ Message string `json:"message"` } `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return "", nil, fmt.Errorf("OpenAI image generation: %s", errResp.Error.Message)
		}
		return "", nil, fmt.Errorf("OpenAI image generation returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || len(result.Data) == 0 {
		return "", nil, fmt.Errorf("failed to parse OpenAI image response")
	}

	out := ""
	var paths []string
	revisedPrompt := ""
	for i, img := range result.Data {
		localURL, imgAbsPath, saveErr := saveImageData(img.B64JSON, "")
		if saveErr != nil {
			return "", nil, fmt.Errorf("failed to save generated image: %w", saveErr)
		}
		paths = append(paths, imgAbsPath)
		if len(result.Data) > 1 {
			out += fmt.Sprintf("![Generated Image %d](%s)\n", i+1, localURL)
		} else {
			out += fmt.Sprintf("![Generated Image](%s)", localURL)
		}
		if img.RevisedPrompt != "" && img.RevisedPrompt != prompt {
			revisedPrompt = img.RevisedPrompt
		}
	}
	if revisedPrompt != "" {
		out += "\n\nRevised prompt: " + revisedPrompt
	}
	return out, paths, nil
}

// imageGenerateGemini calls the Gemini generateContent API for native image generation.
// Returns (markdownResult, absFilePaths, error).
func imageGenerateGemini(prompt, size, model string) (string, []string, error) {
	bundle, _ := creds.Read()
	if bundle.GeminiAPIKey == "" {
		return "", nil, fmt.Errorf("Gemini API key not configured — add it in Settings → Credentials")
	}

	// Gemini uses aspect ratios, not pixel dimensions. Map common OpenAI sizes
	// to their closest aspect ratio; accept bare ratio strings directly.
	aspectRatio := geminiAspectRatio(size)

	payload := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"IMAGE"},
			"imageConfig":        map[string]any{"aspectRatio": aspectRatio},
		},
	}
	body, _ := json.Marshal(payload)

	geminiURL := "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent?key=" + bundle.GeminiAPIKey
	req, err := http.NewRequest("POST", geminiURL, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := newWebClient(90 * time.Second).Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return "", nil, fmt.Errorf("Gemini image generation: %s", errResp.Error.Message)
		}
		return "", nil, fmt.Errorf("Gemini image generation returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("failed to parse Gemini image response: %w", err)
	}

	out := ""
	var paths []string
	imgIdx := 0
	for _, candidate := range result.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			localURL, imgAbsPath, saveErr := saveImageData(part.InlineData.Data, "")
			if saveErr != nil {
				return "", nil, fmt.Errorf("failed to save Gemini image: %w", saveErr)
			}
			paths = append(paths, imgAbsPath)
			imgIdx++
			if imgIdx > 1 {
				out += fmt.Sprintf("\n![Generated Image %d](%s)", imgIdx, localURL)
			} else {
				out += fmt.Sprintf("![Generated Image](%s)", localURL)
			}
		}
	}
	if out == "" {
		return "", nil, fmt.Errorf("Gemini returned no image data")
	}
	return out, paths, nil
}

// imageEditGemini edits an existing image using the Gemini generateContent API.
// prepared must be a PNG-encoded image (output of prepareImageForEdit).
// Returns (markdownResult, absFilePath, error).
func imageEditGemini(prepared []byte, prompt, size, model string) (string, string, error) {
	bundle, _ := creds.Read()
	if bundle.GeminiAPIKey == "" {
		return "", "", fmt.Errorf("Gemini API key not configured — add it in Settings → Credentials")
	}

	aspectRatio := geminiAspectRatio(size)
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": prompt},
					{"inline_data": map[string]any{
						"mime_type": "image/png",
						"data":      base64.StdEncoding.EncodeToString(prepared),
					}},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
			"imageConfig":        map[string]any{"aspectRatio": aspectRatio},
		},
	}
	body, _ := json.Marshal(payload)

	url := "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent?key=" + bundle.GeminiAPIKey
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := newWebClient(90 * time.Second).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return "", "", fmt.Errorf("Gemini image edit: %s", errResp.Error.Message)
		}
		return "", "", fmt.Errorf("Gemini image edit returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse Gemini image edit response: %w", err)
	}
	for _, candidate := range result.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			localURL, absPath, saveErr := saveImageData(part.InlineData.Data, "")
			if saveErr != nil {
				return "", "", fmt.Errorf("failed to save Gemini edited image: %w", saveErr)
			}
			return fmt.Sprintf("![Edited Image](%s)", localURL), absPath, nil
		}
	}
	return "", "", fmt.Errorf("Gemini returned no edited image data")
}

// prepareImageForEdit decodes any supported image, center-crops to square,
// scales to 1024×1024, and encodes as PNG. Called once at the top of imageEdit
// before routing to any provider to normalise input and minimise payload size.
func prepareImageForEdit(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open image: %w", err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("cannot decode image: %w", err)
	}
	cropped := centerCropSquare(src)
	scaled := scaleNN(cropped, 1024, 1024)
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return nil, fmt.Errorf("png encode failed: %w", err)
	}
	return buf.Bytes(), nil
}

// centerCropSquare returns a square crop centred on src.
func centerCropSquare(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == h {
		return src
	}
	side := w
	if h < w {
		side = h
	}
	x0 := b.Min.X + (w-side)/2
	y0 := b.Min.Y + (h-side)/2
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		return si.SubImage(image.Rect(x0, y0, x0+side, y0+side))
	}
	dst := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			dst.Set(x, y, src.At(x0+x, y0+y))
		}
	}
	return dst
}

// scaleNN returns a new RGBA image scaled to w×h using nearest-neighbour interpolation.
func scaleNN(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*srcH/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*srcW/w
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// geminiAspectRatio maps a size hint to a Gemini imageConfig aspectRatio string.
func geminiAspectRatio(size string) string {
	switch size {
	case "1792x1024", "16:9":
		return "16:9"
	case "1024x1792", "9:16":
		return "9:16"
	case "4:3":
		return "4:3"
	case "3:4":
		return "3:4"
	default:
		return "1:1"
	}
}

// saveImageData decodes a base64 string or downloads from url, saves to the
// generated images directory, and returns (relativeURL, absolutePath, error).
// Prefer b64 over url when both are provided.
func saveImageData(b64data, fallbackURL string) (string, string, error) {
	var data []byte

	if b64data != "" {
		decoded, err := base64.StdEncoding.DecodeString(b64data)
		if err != nil {
			return "", "", fmt.Errorf("base64 decode failed: %w", err)
		}
		data = decoded
	} else if fallbackURL != "" {
		resp, err := newWebClient(30 * time.Second).Get(fallbackURL)
		if err != nil {
			return "", "", fmt.Errorf("image download failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", "", fmt.Errorf("image download returned HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
		if err != nil {
			return "", "", fmt.Errorf("image read failed: %w", err)
		}
	} else {
		return "", "", fmt.Errorf("no image data or URL provided")
	}

	ext := imageExt(data)
	filename := uuid.New().String() + ext
	dest := filepath.Join(config.GeneratedImagesDir(), filename)
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", "", fmt.Errorf("save failed: %w", err)
	}
	return "/files/images/" + filename, dest, nil
}

// imageExt sniffs the first bytes of image data and returns the correct file extension.
func imageExt(data []byte) string {
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return ".png"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return ".jpg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp"
	}
	return ".png" // safe default — most providers return PNG
}
