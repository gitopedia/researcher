// Package main provides a utility to generate missing _medium variants for existing images
//
// Usage:
//
//	go run ./cmd/gen-medium-variants --repo-path ../gitopedia
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

func main() {
	repoPath := flag.String("repo-path", "", "Path to gitopedia repository")
	dryRun := flag.Bool("dry-run", false, "Only show what would be done, don't create files")
	flag.Parse()

	if *repoPath == "" {
		log.Fatal("--repo-path is required")
	}

	compendiumPath := filepath.Join(*repoPath, "Compendium")
	if _, err := os.Stat(compendiumPath); os.IsNotExist(err) {
		log.Fatalf("Compendium directory not found at %s", compendiumPath)
	}

	var created, skipped, errors int

	// Walk the Compendium directory looking for header images without medium variants
	err := filepath.Walk(compendiumPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		if info.IsDir() {
			return nil
		}

		// Only process header PNG images that aren't already medium variants
		if !strings.HasSuffix(path, "_header.png") || strings.Contains(path, "-medium") {
			return nil
		}

		// Check if medium variant exists (using hyphen to match website expectations)
		mediumPath := strings.TrimSuffix(path, ".png") + "-medium.png"
		if _, err := os.Stat(mediumPath); err == nil {
			skipped++
			return nil // Medium variant already exists
		}

		// Generate medium variant
		relPath, _ := filepath.Rel(*repoPath, path)
		relMedium, _ := filepath.Rel(*repoPath, mediumPath)

		if *dryRun {
			fmt.Printf("[DRY-RUN] Would create: %s\n", relMedium)
			created++
			return nil
		}

		// Read and resize the image
		imageData, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Error reading %s: %v", relPath, err)
			errors++
			return nil
		}

		mediumData, err := resizeImage(imageData, 960)
		if err != nil {
			log.Printf("Error resizing %s: %v", relPath, err)
			errors++
			return nil
		}

		// Write the medium variant
		if err := os.WriteFile(mediumPath, mediumData, 0644); err != nil {
			log.Printf("Error writing %s: %v", relMedium, err)
			errors++
			return nil
		}

		fmt.Printf("Created: %s\n", relMedium)
		created++
		return nil
	})

	if err != nil {
		log.Fatalf("Error walking directory: %v", err)
	}

	fmt.Printf("\nSummary: %d created, %d skipped (already exist), %d errors\n", created, skipped, errors)
}

// resizeImage resizes a PNG image to the specified width while maintaining aspect ratio
func resizeImage(imageData []byte, targetWidth int) ([]byte, error) {
	// Decode the original image
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Calculate new dimensions maintaining aspect ratio
	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	if origWidth <= targetWidth {
		// Image is already smaller than target, return original
		return imageData, nil
	}

	newWidth := targetWidth
	newHeight := int(float64(origHeight) * float64(targetWidth) / float64(origWidth))

	// Create destination image
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Resize using high-quality CatmullRom interpolation
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("failed to encode resized image: %w", err)
	}

	return buf.Bytes(), nil
}
