package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/image/webp"
)

const (
	maxContactQRCodeBytes     = 2 * 1024 * 1024
	maxContactQRCodeDimension = 4096
)

// normalizeContactQRCode validates the actual image before publishing it on
// anonymous pages. Only inline raster images are accepted; URLs and SVG are
// deliberately excluded. This does not change the existing site-logo policy.
func normalizeContactQRCode(value string) (string, error) {
	if len(value) > base64.StdEncoding.EncodedLen(maxContactQRCodeBytes)+len("data:image/jpeg;base64,") {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image must not exceed 2 MiB")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	header, encoded, ok := strings.Cut(value, ",")
	if !ok {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code must be a PNG, JPEG or WebP base64 data URL")
	}
	var decodeConfig func(io.Reader) (image.Config, error)
	var decode func(io.Reader) (image.Image, error)
	switch header {
	case "data:image/png;base64":
		decodeConfig, decode = png.DecodeConfig, png.Decode
	case "data:image/jpeg;base64":
		decodeConfig, decode = jpeg.DecodeConfig, jpeg.Decode
	case "data:image/webp;base64":
		decodeConfig, decode = webp.DecodeConfig, webp.Decode
	default:
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code must be a PNG, JPEG or WebP base64 data URL")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image data is invalid")
	}
	if len(raw) > maxContactQRCodeBytes {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image must not exceed 2 MiB")
	}
	// Decode dimensions before allocating pixels, then require a complete image
	// so a valid header with a truncated or mismatched payload cannot be saved.
	cfg, err := decodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image does not match its PNG, JPEG or WebP type")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxContactQRCodeDimension || cfg.Height > maxContactQRCodeDimension {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image dimensions must be between 1 and 4096 pixels")
	}
	if _, err := decode(bytes.NewReader(raw)); err != nil {
		return "", infraerrors.BadRequest("INVALID_CONTACT_QR_CODE", "Contact QR code image is incomplete or invalid")
	}
	return header + "," + base64.StdEncoding.EncodeToString(raw), nil
}
