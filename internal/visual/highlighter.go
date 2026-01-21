// Package visual provides visual citation services for highlighting and cropping document images.
package visual

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/fogleman/gg"
	"github.com/google/uuid"
)

// BoundingBox represents a rectangular region on a page image.
type BoundingBox struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PageWidth  float64 `json:"page_width,omitempty"`
	PageHeight float64 `json:"page_height,omitempty"`
}

// HighlightOptions configures how highlights are rendered.
type HighlightOptions struct {
	// Color is the RGBA color for the highlight fill
	Color color.RGBA
	// BorderColor is the RGBA color for the highlight border
	BorderColor color.RGBA
	// BorderWidth is the stroke width for the border (default: 2)
	BorderWidth float64
	// Opacity is the fill opacity (0-1, default: 0.3)
	Opacity float64
	// AddLabel indicates whether to add a citation number label
	AddLabel bool
	// LabelText is the text for the citation label (e.g., "1", "2")
	LabelText string
	// LabelFontSize is the font size for labels (default: 14)
	LabelFontSize float64
}

// DefaultHighlightOptions returns sensible defaults for highlighting.
func DefaultHighlightOptions() HighlightOptions {
	return HighlightOptions{
		Color:         color.RGBA{R: 255, G: 235, B: 59, A: 255},  // Yellow
		BorderColor:   color.RGBA{R: 245, G: 158, B: 11, A: 255},  // Orange
		BorderWidth:   2,
		Opacity:       0.3,
		AddLabel:      true,
		LabelFontSize: 14,
	}
}

// HighlightResult contains the result of a highlight operation.
type HighlightResult struct {
	// ImagePath is the storage path of the highlighted image
	ImagePath string `json:"image_path"`
	// ImageURL is the public URL of the highlighted image
	ImageURL string `json:"image_url"`
	// Width is the image width in pixels
	Width int `json:"width"`
	// Height is the image height in pixels
	Height int `json:"height"`
}

// RegionWithLabel represents a bounding box with an optional label.
type RegionWithLabel struct {
	BoundingBox BoundingBox
	Label       string
	Color       color.RGBA
}

// Highlighter provides image highlighting services for visual citations.
type Highlighter struct {
	storage storage.ObjectStorage
	log     *logger.Logger
}

// NewHighlighter creates a new Highlighter instance.
func NewHighlighter(store storage.ObjectStorage, log *logger.Logger) *Highlighter {
	if log == nil {
		log = logger.Default()
	}
	return &Highlighter{
		storage: store,
		log:     log.WithComponent("highlighter"),
	}
}

// HighlightRegion highlights a single region on an image and uploads the result.
func (h *Highlighter) HighlightRegion(
	ctx context.Context,
	imagePath string,
	bbox BoundingBox,
	opts HighlightOptions,
	queryID string,
	chunkID string,
) (*HighlightResult, error) {
	h.log.Info("highlighting region",
		"image_path", imagePath,
		"bbox", fmt.Sprintf("%.0fx%.0f at (%.0f,%.0f)", bbox.Width, bbox.Height, bbox.X, bbox.Y),
	)

	// Download the original image
	imgBytes, err := h.storage.Download(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Create drawing context
	dc := gg.NewContextForImage(img)
	bounds := img.Bounds()

	// Draw the highlight
	h.drawHighlight(dc, bbox, opts)

	// Add label if requested
	if opts.AddLabel && opts.LabelText != "" {
		h.drawLabel(dc, bbox, opts.LabelText, opts.LabelFontSize)
	}

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return nil, fmt.Errorf("failed to encode highlighted image: %w", err)
	}

	// Generate storage path
	storagePath := storage.BuildHighlightedPath(queryID, chunkID)

	// Upload the highlighted image
	uploadedPath, err := h.storage.UploadBytes(ctx, buf.Bytes(), storagePath, "image/png")
	if err != nil {
		return nil, fmt.Errorf("failed to upload highlighted image: %w", err)
	}

	// Get the URL
	url, err := h.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		h.log.WithError(err).Warn("failed to get highlighted image URL")
		url = ""
	}

	h.log.Info("region highlighted successfully",
		"output_path", uploadedPath,
	)

	return &HighlightResult{
		ImagePath: uploadedPath,
		ImageURL:  url,
		Width:     bounds.Dx(),
		Height:    bounds.Dy(),
	}, nil
}

// HighlightMultipleRegions highlights multiple regions on a single image.
// Each region can have its own label and color.
func (h *Highlighter) HighlightMultipleRegions(
	ctx context.Context,
	imagePath string,
	regions []RegionWithLabel,
	queryID string,
) (*HighlightResult, error) {
	if len(regions) == 0 {
		return nil, fmt.Errorf("no regions provided")
	}

	h.log.Info("highlighting multiple regions",
		"image_path", imagePath,
		"region_count", len(regions),
	)

	// Download the original image
	imgBytes, err := h.storage.Download(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Create drawing context
	dc := gg.NewContextForImage(img)
	bounds := img.Bounds()

	// Draw each region with its assigned color and label
	colors := h.getHighlightColors()
	for i, region := range regions {
		opts := DefaultHighlightOptions()

		// Use provided color or cycle through default colors
		if region.Color.A > 0 {
			opts.Color = region.Color
		} else if i < len(colors) {
			opts.Color = colors[i]
		}

		// Set border color slightly darker than fill
		opts.BorderColor = h.darkenColor(opts.Color)

		// Draw the highlight
		h.drawHighlight(dc, region.BoundingBox, opts)

		// Add label
		if region.Label != "" {
			h.drawLabel(dc, region.BoundingBox, region.Label, opts.LabelFontSize)
		}
	}

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return nil, fmt.Errorf("failed to encode highlighted image: %w", err)
	}

	// Generate a unique ID for the combined highlight image
	combinedID := uuid.New().String()[:8]
	storagePath := storage.BuildHighlightedPath(queryID, "combined-"+combinedID)

	// Upload the highlighted image
	uploadedPath, err := h.storage.UploadBytes(ctx, buf.Bytes(), storagePath, "image/png")
	if err != nil {
		return nil, fmt.Errorf("failed to upload highlighted image: %w", err)
	}

	// Get the URL
	url, err := h.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		h.log.WithError(err).Warn("failed to get highlighted image URL")
		url = ""
	}

	h.log.Info("multiple regions highlighted successfully",
		"output_path", uploadedPath,
		"region_count", len(regions),
	)

	return &HighlightResult{
		ImagePath: uploadedPath,
		ImageURL:  url,
		Width:     bounds.Dx(),
		Height:    bounds.Dy(),
	}, nil
}

// HighlightRegionBytes highlights a region on an image provided as bytes.
// Returns the highlighted image as bytes without uploading to storage.
func (h *Highlighter) HighlightRegionBytes(
	imgBytes []byte,
	bbox BoundingBox,
	opts HighlightOptions,
) ([]byte, error) {
	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Create drawing context
	dc := gg.NewContextForImage(img)

	// Draw the highlight
	h.drawHighlight(dc, bbox, opts)

	// Add label if requested
	if opts.AddLabel && opts.LabelText != "" {
		h.drawLabel(dc, bbox, opts.LabelText, opts.LabelFontSize)
	}

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return nil, fmt.Errorf("failed to encode highlighted image: %w", err)
	}

	return buf.Bytes(), nil
}

// drawHighlight draws a single highlight rectangle on the context.
func (h *Highlighter) drawHighlight(dc *gg.Context, bbox BoundingBox, opts HighlightOptions) {
	// Clamp coordinates to image bounds
	x := max(0, bbox.X)
	y := max(0, bbox.Y)
	w := bbox.Width
	height := bbox.Height

	// Clamp to context bounds
	imgW := float64(dc.Width())
	imgH := float64(dc.Height())
	if x+w > imgW {
		w = imgW - x
	}
	if y+height > imgH {
		height = imgH - y
	}

	// Draw fill with transparency
	fillColor := opts.Color
	dc.SetRGBA(
		float64(fillColor.R)/255.0,
		float64(fillColor.G)/255.0,
		float64(fillColor.B)/255.0,
		opts.Opacity,
	)
	dc.DrawRectangle(x, y, w, height)
	dc.Fill()

	// Draw border
	borderColor := opts.BorderColor
	dc.SetRGBA(
		float64(borderColor.R)/255.0,
		float64(borderColor.G)/255.0,
		float64(borderColor.B)/255.0,
		1.0,
	)
	dc.SetLineWidth(opts.BorderWidth)
	dc.DrawRectangle(x, y, w, height)
	dc.Stroke()
}

// drawLabel draws a citation number label near the highlight.
func (h *Highlighter) drawLabel(dc *gg.Context, bbox BoundingBox, label string, fontSize float64) {
	if fontSize <= 0 {
		fontSize = 14
	}

	// Calculate label position (top-left of bounding box)
	labelX := bbox.X - 2
	labelY := bbox.Y - 2

	// Ensure label is within image bounds
	if labelY < fontSize+4 {
		labelY = bbox.Y + fontSize + 4
	}
	if labelX < 2 {
		labelX = bbox.X + 2
	}

	// Draw label background (dark circle)
	circleRadius := fontSize * 0.8
	circleX := labelX + circleRadius
	circleY := labelY - circleRadius/2

	// Ensure circle is within bounds
	if circleX < circleRadius {
		circleX = circleRadius + 2
	}
	if circleY < circleRadius {
		circleY = circleRadius + 2
	}

	dc.SetRGBA(0.2, 0.2, 0.2, 0.9) // Dark background
	dc.DrawCircle(circleX, circleY, circleRadius)
	dc.Fill()

	// Draw border around the circle
	dc.SetRGBA(1, 1, 1, 1) // White border
	dc.SetLineWidth(1)
	dc.DrawCircle(circleX, circleY, circleRadius)
	dc.Stroke()

	// Draw label text
	dc.SetRGBA(1, 1, 1, 1) // White text
	textWidth, _ := dc.MeasureString(label)
	textX := circleX - textWidth/2
	textY := circleY + fontSize/3 // Vertical centering approximation

	dc.DrawString(label, textX, textY)
}

// getHighlightColors returns a palette of colors for multiple highlights.
func (h *Highlighter) getHighlightColors() []color.RGBA {
	return []color.RGBA{
		{R: 255, G: 235, B: 59, A: 255},  // Yellow
		{R: 76, G: 175, B: 80, A: 255},   // Green
		{R: 33, G: 150, B: 243, A: 255},  // Blue
		{R: 156, G: 39, B: 176, A: 255},  // Purple
		{R: 255, G: 152, B: 0, A: 255},   // Orange
		{R: 0, G: 188, B: 212, A: 255},   // Cyan
		{R: 233, G: 30, B: 99, A: 255},   // Pink
		{R: 139, G: 195, B: 74, A: 255},  // Light Green
		{R: 255, G: 87, B: 34, A: 255},   // Deep Orange
		{R: 103, G: 58, B: 183, A: 255},  // Deep Purple
	}
}

// darkenColor creates a darker version of a color for borders.
func (h *Highlighter) darkenColor(c color.RGBA) color.RGBA {
	factor := 0.7
	return color.RGBA{
		R: uint8(float64(c.R) * factor),
		G: uint8(float64(c.G) * factor),
		B: uint8(float64(c.B) * factor),
		A: c.A,
	}
}

// ValidateBoundingBox checks if a bounding box has valid dimensions.
func ValidateBoundingBox(bbox BoundingBox) error {
	if bbox.Width <= 0 {
		return fmt.Errorf("bounding box width must be positive, got %.2f", bbox.Width)
	}
	if bbox.Height <= 0 {
		return fmt.Errorf("bounding box height must be positive, got %.2f", bbox.Height)
	}
	if bbox.X < 0 {
		return fmt.Errorf("bounding box X must be non-negative, got %.2f", bbox.X)
	}
	if bbox.Y < 0 {
		return fmt.Errorf("bounding box Y must be non-negative, got %.2f", bbox.Y)
	}
	return nil
}

// ScaleBoundingBox scales a bounding box from document coordinates to image coordinates.
// This is useful when the bounding box was extracted from PDF coordinates but needs
// to be applied to a rendered image at a different resolution.
func ScaleBoundingBox(bbox BoundingBox, targetWidth, targetHeight float64) BoundingBox {
	if bbox.PageWidth <= 0 || bbox.PageHeight <= 0 {
		return bbox
	}

	scaleX := targetWidth / bbox.PageWidth
	scaleY := targetHeight / bbox.PageHeight

	return BoundingBox{
		X:          bbox.X * scaleX,
		Y:          bbox.Y * scaleY,
		Width:      bbox.Width * scaleX,
		Height:     bbox.Height * scaleY,
		PageWidth:  targetWidth,
		PageHeight: targetHeight,
	}
}

// NormalizeBoundingBox converts a bounding box to normalized coordinates (0-1).
func NormalizeBoundingBox(bbox BoundingBox) BoundingBox {
	if bbox.PageWidth <= 0 || bbox.PageHeight <= 0 {
		return bbox
	}

	return BoundingBox{
		X:          bbox.X / bbox.PageWidth,
		Y:          bbox.Y / bbox.PageHeight,
		Width:      bbox.Width / bbox.PageWidth,
		Height:     bbox.Height / bbox.PageHeight,
		PageWidth:  1.0,
		PageHeight: 1.0,
	}
}

// DenormalizeBoundingBox converts normalized coordinates to absolute coordinates.
func DenormalizeBoundingBox(bbox BoundingBox, pageWidth, pageHeight float64) BoundingBox {
	return BoundingBox{
		X:          bbox.X * pageWidth,
		Y:          bbox.Y * pageHeight,
		Width:      bbox.Width * pageWidth,
		Height:     bbox.Height * pageHeight,
		PageWidth:  pageWidth,
		PageHeight: pageHeight,
	}
}

// ParseHighlightColor parses a color string (hex or named) into color.RGBA.
func ParseHighlightColor(colorStr string) (color.RGBA, error) {
	colorStr = strings.TrimPrefix(strings.ToLower(colorStr), "#")

	// Named colors
	namedColors := map[string]color.RGBA{
		"yellow":  {R: 255, G: 235, B: 59, A: 255},
		"green":   {R: 76, G: 175, B: 80, A: 255},
		"blue":    {R: 33, G: 150, B: 243, A: 255},
		"red":     {R: 244, G: 67, B: 54, A: 255},
		"orange":  {R: 255, G: 152, B: 0, A: 255},
		"purple":  {R: 156, G: 39, B: 176, A: 255},
		"cyan":    {R: 0, G: 188, B: 212, A: 255},
		"pink":    {R: 233, G: 30, B: 99, A: 255},
	}

	if c, ok := namedColors[colorStr]; ok {
		return c, nil
	}

	// Parse hex color
	if len(colorStr) == 6 {
		var r, g, b uint8
		_, err := fmt.Sscanf(colorStr, "%02x%02x%02x", &r, &g, &b)
		if err != nil {
			return color.RGBA{}, fmt.Errorf("invalid hex color: %s", colorStr)
		}
		return color.RGBA{R: r, G: g, B: b, A: 255}, nil
	}

	return color.RGBA{}, fmt.Errorf("invalid color format: %s", colorStr)
}
