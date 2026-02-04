#!/usr/bin/env node
/**
 * Converts PNG images to AVIF format.
 * Called by the finalize endpoint after image selection.
 * 
 * Usage: node convert-to-avif.js <png-file-path> [<png-file-path> ...]
 * 
 * For each PNG file:
 * - Creates full size AVIF: <filename>.avif
 * - Creates medium (50%) AVIF: <filename>-medium.avif
 * - Deletes the original PNG
 */

const fs = require('fs');
const path = require('path');

// Use sharp from the website folder (has compatible version)
const websiteNodeModules = path.resolve(__dirname, '../../website/node_modules/sharp');
const sharp = require(websiteNodeModules);

async function convertImage(pngPath) {
  if (!fs.existsSync(pngPath)) {
    console.error(`File not found: ${pngPath}`);
    return { success: false, error: 'File not found' };
  }

  const dir = path.dirname(pngPath);
  const basename = path.basename(pngPath, '.png');
  
  const fullAvifPath = path.join(dir, `${basename}.avif`);
  const mediumAvifPath = path.join(dir, `${basename}-medium.avif`);
  
  try {
    // Get image metadata for dimensions
    const metadata = await sharp(pngPath).metadata();
    const { width, height } = metadata;
    
    // Convert to full-size AVIF
    await sharp(pngPath)
      .avif({ quality: 80 })
      .toFile(fullAvifPath);
    
    // Convert to medium (50%) AVIF
    const mediumWidth = Math.round(width / 2);
    const mediumHeight = Math.round(height / 2);
    
    await sharp(pngPath)
      .resize(mediumWidth, mediumHeight)
      .avif({ quality: 80 })
      .toFile(mediumAvifPath);
    
    // Remove original PNG
    fs.unlinkSync(pngPath);
    
    console.log(JSON.stringify({
      success: true,
      source: pngPath,
      outputs: [
        { path: fullAvifPath, width, height },
        { path: mediumAvifPath, width: mediumWidth, height: mediumHeight }
      ]
    }));
    
    return { success: true };
  } catch (error) {
    console.error(JSON.stringify({
      success: false,
      source: pngPath,
      error: error.message
    }));
    return { success: false, error: error.message };
  }
}

async function main() {
  const args = process.argv.slice(2);
  
  if (args.length === 0) {
    console.error('Usage: node convert-to-avif.js <png-file-path> [<png-file-path> ...]');
    process.exit(1);
  }
  
  let successCount = 0;
  let failCount = 0;
  
  for (const pngPath of args) {
    const result = await convertImage(pngPath);
    if (result.success) {
      successCount++;
    } else {
      failCount++;
    }
  }
  
  // Exit with error code if any conversions failed
  process.exit(failCount > 0 ? 1 : 0);
}

main();
