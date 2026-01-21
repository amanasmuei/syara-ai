package visual

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/fogleman/gg"
)

// CropOptions configures how images are cropped.
type CropOptions struct {
	// Padding is the number of pixels to add around the region (default: 20)
	Padding int
	// MinWidth is the minimum width of the cropped region (default: 100)
	MinWidth int
	// MinHeight is the minimum height of the cropped region (default: 50)
	MinHeight int
	// AddHighlight adds a highlight to the cropped region
	AddHighlight bool
	// HighlightOpts configures the highlight (if AddHighlight is true)
	HighlightOpts HighlightOptions
	// MaintainAspectRatio keeps the original aspect ratio when expanding to minimum size
	MaintainAspectRatio bool
}

// DefaultCropOptions returns sensible defaults for cropping.
func DefaultCropOptions() CropOptions {
	return CropOptions{
		Padding:             20,
		MinWidth:            100,
		MinHeight:           50,
		AddHighlight:        false,
		HighlightOpts:       DefaultHighlightOptions(),
		MaintainAspectRatio: true,
	}
}

// CropResult contains the result of a crop operation.
type CropResult struct {
	// ImagePath is the storage path of the cropped image
	ImagePath string `json:"image_path"`
	// ImageURL is the public URL of the cropped image
	ImageURL string `json:"image_url"`
	// Width is the cropped image width in pixels
	Width int `json:"width"`
	// Height is the cropped image height in pixels
	Height int `json:"height"`
	// CropRect is the rectangle that was actually cropped
	CropRect image.Rectangle `json:"crop_rect"`
	// OriginalBBox is the original bounding box translated to crop coordinates
	OriginalBBox BoundingBox `json:"original_bbox"`
}

// Cropper provides image cropping services for visual citations.
type Cropper struct {
	storage storage.ObjectStorage
	log     *logger.Logger
}

// NewCropper creates a new Cropper instance.
func NewCropper(store storage.ObjectStorage, log *logger.Logger) *Cropper {
	if log == nil {
		log = logger.Default()
	}
	return &Cropper{
		storage: store,
		log:     log.WithComponent("cropper"),
	}
}

// CropRegion crops an image to show only the specified region with padding.
func (c *Cropper) CropRegion(
	ctx context.Context,
	imagePath string,
	bbox BoundingBox,
	opts CropOptions,
	queryID string,
	chunkID string,
) (*CropResult, error) {
	c.log.Info("cropping region",
		"image_path", imagePath,
		"bbox", fmt.Sprintf("%.0fx%.0f at (%.0f,%.0f)", bbox.Width, bbox.Height, bbox.X, bbox.Y),
		"padding", opts.Padding,
	)

	// Download the original image
	imgBytes, err := c.storage.Download(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()

	// Calculate crop rectangle with padding
	cropRect := c.calculateCropRect(bbox, bounds, opts)

	// Perform the crop
	croppedImg := c.cropImage(img, cropRect)

	// Optionally add highlight to the cropped image
	if opts.AddHighlight {
		// Translate bbox to crop coordinates
		translatedBBox := BoundingBox{
			X:          bbox.X - float64(cropRect.Min.X),
			Y:          bbox.Y - float64(cropRect.Min.Y),
			Width:      bbox.Width,
			Height:     bbox.Height,
			PageWidth:  float64(cropRect.Dx()),
			PageHeight: float64(cropRect.Dy()),
		}
		croppedImg = c.addHighlightToCrop(croppedImg, translatedBBox, opts.HighlightOpts)
	}

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, croppedImg); err != nil {
		return nil, fmt.Errorf("failed to encode cropped image: %w", err)
	}

	// Generate storage path
	storagePath := storage.BuildCroppedPath(queryID, chunkID)

	// Upload the cropped image
	uploadedPath, err := c.storage.UploadBytes(ctx, buf.Bytes(), storagePath, "image/png")
	if err != nil {
		return nil, fmt.Errorf("failed to upload cropped image: %w", err)
	}

	// Get the URL
	url, err := c.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		c.log.WithError(err).Warn("failed to get cropped image URL")
		url = ""
	}

	// Calculate the translated bbox for the result
	translatedBBox := BoundingBox{
		X:          bbox.X - float64(cropRect.Min.X),
		Y:          bbox.Y - float64(cropRect.Min.Y),
		Width:      bbox.Width,
		Height:     bbox.Height,
		PageWidth:  float64(cropRect.Dx()),
		PageHeight: float64(cropRect.Dy()),
	}

	c.log.Info("region cropped successfully",
		"output_path", uploadedPath,
		"crop_size", fmt.Sprintf("%dx%d", cropRect.Dx(), cropRect.Dy()),
	)

	return &CropResult{
		ImagePath:    uploadedPath,
		ImageURL:     url,
		Width:        cropRect.Dx(),
		Height:       cropRect.Dy(),
		CropRect:     cropRect,
		OriginalBBox: translatedBBox,
	}, nil
}

// CropRegionBytes crops an image provided as bytes and returns the cropped image as bytes.
func (c *Cropper) CropRegionBytes(
	imgBytes []byte,
	bbox BoundingBox,
	opts CropOptions,
) ([]byte, *CropResult, error) {
	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()

	// Calculate crop rectangle with padding
	cropRect := c.calculateCropRect(bbox, bounds, opts)

	// Perform the crop
	croppedImg := c.cropImage(img, cropRect)

	// Optionally add highlight to the cropped image
	if opts.AddHighlight {
		translatedBBox := BoundingBox{
			X:          bbox.X - float64(cropRect.Min.X),
			Y:          bbox.Y - float64(cropRect.Min.Y),
			Width:      bbox.Width,
			Height:     bbox.Height,
			PageWidth:  float64(cropRect.Dx()),
			PageHeight: float64(cropRect.Dy()),
		}
		croppedImg = c.addHighlightToCrop(croppedImg, translatedBBox, opts.HighlightOpts)
	}

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, croppedImg); err != nil {
		return nil, nil, fmt.Errorf("failed to encode cropped image: %w", err)
	}

	// Calculate the translated bbox for the result
	translatedBBox := BoundingBox{
		X:          bbox.X - float64(cropRect.Min.X),
		Y:          bbox.Y - float64(cropRect.Min.Y),
		Width:      bbox.Width,
		Height:     bbox.Height,
		PageWidth:  float64(cropRect.Dx()),
		PageHeight: float64(cropRect.Dy()),
	}

	return buf.Bytes(), &CropResult{
		Width:        cropRect.Dx(),
		Height:       cropRect.Dy(),
		CropRect:     cropRect,
		OriginalBBox: translatedBBox,
	}, nil
}

// CropMultipleRegions crops an image to contain all specified regions with padding.
func (c *Cropper) CropMultipleRegions(
	ctx context.Context,
	imagePath string,
	bboxes []BoundingBox,
	opts CropOptions,
	queryID string,
) (*CropResult, error) {
	if len(bboxes) == 0 {
		return nil, fmt.Errorf("no bounding boxes provided")
	}

	c.log.Info("cropping multiple regions",
		"image_path", imagePath,
		"region_count", len(bboxes),
		"padding", opts.Padding,
	)

	// Download the original image
	imgBytes, err := c.storage.Download(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()

	// Find the bounding rectangle that encompasses all regions
	combinedBBox := c.combineBoundingBoxes(bboxes)

	// Calculate crop rectangle with padding
	cropRect := c.calculateCropRect(combinedBBox, bounds, opts)

	// Perform the crop
	croppedImg := c.cropImage(img, cropRect)

	// Encode the result
	var buf bytes.Buffer
	if err := png.Encode(&buf, croppedImg); err != nil {
		return nil, fmt.Errorf("failed to encode cropped image: %w", err)
	}

	// Generate storage path
	storagePath := storage.BuildCroppedPath(queryID, "combined")

	// Upload the cropped image
	uploadedPath, err := c.storage.UploadBytes(ctx, buf.Bytes(), storagePath, "image/png")
	if err != nil {
		return nil, fmt.Errorf("failed to upload cropped image: %w", err)
	}

	// Get the URL
	url, err := c.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		c.log.WithError(err).Warn("failed to get cropped image URL")
		url = ""
	}

	c.log.Info("multiple regions cropped successfully",
		"output_path", uploadedPath,
		"crop_size", fmt.Sprintf("%dx%d", cropRect.Dx(), cropRect.Dy()),
	)

	return &CropResult{
		ImagePath: uploadedPath,
		ImageURL:  url,
		Width:     cropRect.Dx(),
		Height:    cropRect.Dy(),
		CropRect:  cropRect,
	}, nil
}

// calculateCropRect calculates the crop rectangle with padding and minimum size constraints.
func (c *Cropper) calculateCropRect(bbox BoundingBox, bounds image.Rectangle, opts CropOptions) image.Rectangle {
	padding := opts.Padding
	if padding < 0 {
		padding = 0
	}

	// Calculate initial crop rectangle
	x1 := int(bbox.X) - padding
	y1 := int(bbox.Y) - padding
	x2 := int(bbox.X+bbox.Width) + padding
	y2 := int(bbox.Y+bbox.Height) + padding

	// Calculate current dimensions
	width := x2 - x1
	height := y2 - y1

	// Ensure minimum dimensions
	if width < opts.MinWidth {
		diff := opts.MinWidth - width
		x1 -= diff / 2
		x2 += diff - diff/2
	}
	if height < opts.MinHeight {
		diff := opts.MinHeight - height
		y1 -= diff / 2
		y2 += diff - diff/2
	}

	// Clamp to image bounds
	x1 = max(bounds.Min.X, x1)
	y1 = max(bounds.Min.Y, y1)
	x2 = min(bounds.Max.X, x2)
	y2 = min(bounds.Max.Y, y2)

	// Ensure we have valid dimensions after clamping
	if x2 <= x1 {
		x2 = x1 + 1
	}
	if y2 <= y1 {
		y2 = y1 + 1
	}

	return image.Rect(x1, y1, x2, y2)
}

// cropImage performs the actual crop operation.
func (c *Cropper) cropImage(img image.Image, cropRect image.Rectangle) image.Image {
	// Create a new RGBA image with the crop dimensions
	cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))

	// Draw the cropped portion
	draw.Draw(cropped, cropped.Bounds(), img, cropRect.Min, draw.Src)

	return cropped
}

// addHighlightToCrop adds a highlight overlay to a cropped image.
func (c *Cropper) addHighlightToCrop(img image.Image, bbox BoundingBox, opts HighlightOptions) image.Image {
	dc := gg.NewContextForImage(img)

	// Clamp coordinates to image bounds
	x := max(0, bbox.X)
	y := max(0, bbox.Y)
	w := bbox.Width
	h := bbox.Height

	imgW := float64(dc.Width())
	imgH := float64(dc.Height())
	if x+w > imgW {
		w = imgW - x
	}
	if y+h > imgH {
		h = imgH - y
	}

	// Draw fill with transparency
	fillColor := opts.Color
	dc.SetRGBA(
		float64(fillColor.R)/255.0,
		float64(fillColor.G)/255.0,
		float64(fillColor.B)/255.0,
		opts.Opacity,
	)
	dc.DrawRectangle(x, y, w, h)
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
	dc.DrawRectangle(x, y, w, h)
	dc.Stroke()

	return dc.Image()
}

// combineBoundingBoxes creates a single bounding box that encompasses all provided boxes.
func (c *Cropper) combineBoundingBoxes(bboxes []BoundingBox) BoundingBox {
	if len(bboxes) == 0 {
		return BoundingBox{}
	}

	minX := bboxes[0].X
	minY := bboxes[0].Y
	maxX := bboxes[0].X + bboxes[0].Width
	maxY := bboxes[0].Y + bboxes[0].Height

	for _, bbox := range bboxes[1:] {
		if bbox.X < minX {
			minX = bbox.X
		}
		if bbox.Y < minY {
			minY = bbox.Y
		}
		if bbox.X+bbox.Width > maxX {
			maxX = bbox.X + bbox.Width
		}
		if bbox.Y+bbox.Height > maxY {
			maxY = bbox.Y + bbox.Height
		}
	}

	return BoundingBox{
		X:      minX,
		Y:      minY,
		Width:  maxX - minX,
		Height: maxY - minY,
	}
}

// CropAndHighlight is a convenience method that crops an image and adds a highlight.
func (c *Cropper) CropAndHighlight(
	ctx context.Context,
	imagePath string,
	bbox BoundingBox,
	cropOpts CropOptions,
	highlightOpts HighlightOptions,
	queryID string,
	chunkID string,
) (*CropResult, error) {
	cropOpts.AddHighlight = true
	cropOpts.HighlightOpts = highlightOpts
	return c.CropRegion(ctx, imagePath, bbox, cropOpts, queryID, chunkID)
}
