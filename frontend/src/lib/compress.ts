export interface CompressionOptions {
  maxDimension?: number;
  quality?: number;
}

export interface CompressionResult {
  file: File;
  previewUrl: string;
  originalBytes: number;
  compressedBytes: number;
  width: number;
  height: number;
  wasCompressed: boolean;
}

/**
 * Formats a byte size into a human-readable string (e.g., "4.2 MB" or "310 KB").
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 KB';
  if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

/**
 * Compresses and resizes a camera/gallery image using an HTML5 <canvas>.
 * - Preserves aspect ratio up to `maxDimension` (default 1600px).
 * - Uses `createImageBitmap` (when available) or `HTMLImageElement` so modern browsers
 *   automatically honor EXIF orientation tags from mobile cameras.
 * - Falls back gracefully to the original `File` if canvas decoding fails.
 */
export async function compressImageFile(
  originalFile: File,
  options: CompressionOptions = {}
): Promise<CompressionResult> {
  const maxDimension = options.maxDimension ?? 1600;
  const quality = options.quality ?? 0.84;
  const originalBytes = originalFile.size;

  // Keep animated GIFs intact so they don't lose animation frames
  if (originalFile.type === 'image/gif') {
    const previewUrl = URL.createObjectURL(originalFile);
    return {
      file: originalFile,
      previewUrl,
      originalBytes,
      compressedBytes: originalBytes,
      width: 0,
      height: 0,
      wasCompressed: false,
    };
  }

  try {
    const { source, width: srcWidth, height: srcHeight, cleanup } = await decodeImage(originalFile);

    try {
      let targetWidth = srcWidth;
      let targetHeight = srcHeight;

      if (srcWidth > maxDimension || srcHeight > maxDimension) {
        if (srcWidth >= srcHeight) {
          targetWidth = maxDimension;
          targetHeight = Math.max(1, Math.round((srcHeight * maxDimension) / srcWidth));
        } else {
          targetHeight = maxDimension;
          targetWidth = Math.max(1, Math.round((srcWidth * maxDimension) / srcHeight));
        }
      }

      const canvas = document.createElement('canvas');
      canvas.width = targetWidth;
      canvas.height = targetHeight;

      const ctx = canvas.getContext('2d');
      if (!ctx) {
        throw new Error('Canvas 2D context unavailable');
      }

      // Fill warm white base in case a transparent PNG is converted to JPEG
      ctx.fillStyle = '#FAF6F0';
      ctx.fillRect(0, 0, targetWidth, targetHeight);
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = 'high';
      ctx.drawImage(source, 0, 0, targetWidth, targetHeight);

      const blob = await new Promise<Blob | null>((resolve) => {
        canvas.toBlob((b) => resolve(b), 'image/jpeg', quality);
      });

      if (!blob) {
        throw new Error('Canvas toBlob returned null');
      }

      const baseName = originalFile.name.replace(/\.[^/.]+$/, '') || 'memory';
      const compressedFile = new File([blob], `${baseName}.jpg`, {
        type: 'image/jpeg',
        lastModified: Date.now(),
      });

      // If the original file was already a small JPEG/WebP and canvas made it larger without resizing, keep original
      const didResize = targetWidth !== srcWidth || targetHeight !== srcHeight;
      const isSupportedDirect =
        originalFile.type === 'image/jpeg' ||
        originalFile.type === 'image/png' ||
        originalFile.type === 'image/webp';

      if (!didResize && isSupportedDirect && compressedFile.size >= originalFile.size) {
        const previewUrl = URL.createObjectURL(originalFile);
        return {
          file: originalFile,
          previewUrl,
          originalBytes,
          compressedBytes: originalFile.size,
          width: srcWidth,
          height: srcHeight,
          wasCompressed: false,
        };
      }

      const previewUrl = URL.createObjectURL(compressedFile);
      return {
        file: compressedFile,
        previewUrl,
        originalBytes,
        compressedBytes: compressedFile.size,
        width: targetWidth,
        height: targetHeight,
        wasCompressed: true,
      };
    } finally {
      cleanup();
    }
  } catch {
    // Graceful fallback to original file
    const previewUrl = URL.createObjectURL(originalFile);
    return {
      file: originalFile,
      previewUrl,
      originalBytes,
      compressedBytes: originalBytes,
      width: 0,
      height: 0,
      wasCompressed: false,
    };
  }
}

interface DecodedSource {
  source: CanvasImageSource;
  width: number;
  height: number;
  cleanup: () => void;
}

async function decodeImage(file: File): Promise<DecodedSource> {
  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(file, {
        imageOrientation: 'from-image',
      } as ImageBitmapOptions);
      return {
        source: bitmap,
        width: bitmap.width,
        height: bitmap.height,
        cleanup: () => bitmap.close(),
      };
    } catch {
      // Fallback to <img> element decoding
    }
  }

  return new Promise<DecodedSource>((resolve, reject) => {
    const objectUrl = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      resolve({
        source: img,
        width: img.naturalWidth || img.width,
        height: img.naturalHeight || img.height,
        cleanup: () => URL.revokeObjectURL(objectUrl),
      });
    };
    img.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      reject(new Error('Failed to load image for compression'));
    };
    img.src = objectUrl;
  });
}
