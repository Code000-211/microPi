import { getExifOrientation } from "./exif-orientation.ts";
import { encodeWithinLimits, type ImageResizeOptions, type ResizedImage } from "./image-resize-core.ts";

/**
 * The part of Bun's `Bun.Image` (Bun 1.4+) used here. Pi does not depend on Bun's type package,
 * so the shape is declared locally; it matches `packages/bun-types/bun.d.ts` of the pinned runtime.
 */
interface NativeImagePipeline {
	resize(width: number, height: number, options: { filter: "lanczos3" | "box"; fit: "fill" }): NativeImagePipeline;
	rotate(degrees: number): NativeImagePipeline;
	flip(): NativeImagePipeline;
	flop(): NativeImagePipeline;
	png(): NativeImagePipeline;
	jpeg(options: { quality: number }): NativeImagePipeline;
	bytes(): Promise<Uint8Array>;
}

export type NativeImageConstructor = new (input: Uint8Array, options: { autoOrient: boolean }) => NativeImagePipeline;

/** `Bun.Image` when the runtime has it (Bun, including µPi's runtime), else undefined (Node). */
export function getNativeImage(): NativeImageConstructor | undefined {
	const image = (globalThis as { Bun?: { Image?: unknown } }).Bun?.Image;
	return typeof image === "function" ? (image as NativeImageConstructor) : undefined;
}

/**
 * The EXIF orientation as the transforms Bun.Image applies in its fixed order (rotate, then flip
 * or flop), matching applyExifOrientation's photon transforms pixel for pixel.
 */
function orient(pipeline: NativeImagePipeline, orientation: number): NativeImagePipeline {
	switch (orientation) {
		case 2:
			return pipeline.flop();
		case 3:
			return pipeline.rotate(180);
		case 4:
			return pipeline.flip();
		case 5:
			return pipeline.rotate(90).flop();
		case 6:
			return pipeline.rotate(90);
		case 7:
			return pipeline.rotate(270).flop();
		case 8:
			return pipeline.rotate(270);
		default:
			return pipeline;
	}
}

/**
 * Resize with the runtime's native image code (libspng, libjpeg-turbo and libwebp decoders, SIMD
 * Lanczos3 resize, libspng and libjpeg-turbo encoders) instead of photon's WebAssembly, which the
 * JIT-off µPi runtime interprets: a 2442x1438 screenshot takes about 0.5 s of CPU instead of
 * about 15 s. It runs on Bun's own worker threads, so the event loop stays free.
 *
 * Same output rules as the photon path (same target sizes, candidate order and size limit), but
 * not the same bytes: the resampling rounds differently and the encoders differ.
 *
 * `width` and `height` are the displayed size of the input (after EXIF orientation), from its
 * header. Throws when the native decoder or encoder fails; the caller then uses photon.
 */
export function resizeImageNative(
	NativeImage: NativeImageConstructor,
	inputBytes: Uint8Array,
	width: number,
	height: number,
	opts: Required<ImageResizeOptions>,
): Promise<ResizedImage | null> {
	const orientation = getExifOrientation(inputBytes);
	return encodeWithinLimits(width, height, opts, (targetWidth, targetHeight) => {
		const pipeline = () =>
			orient(new NativeImage(inputBytes, { autoOrient: false }), orientation).resize(targetWidth, targetHeight, {
				filter: "lanczos3",
				fit: "fill",
			});
		return {
			png: () => pipeline().png().bytes(),
			jpeg: (quality) => pipeline().jpeg({ quality }).bytes(),
			free: () => {},
		};
	});
}

/**
 * Whether the native decoder decodes the whole image without an error. A full decode to a 1x1 box
 * average: about 4-6 ms for a 1600x1200 JPEG or a 1800x1100 PNG, 25-35 ms for WebP and GIF.
 */
export async function decodesNatively(NativeImage: NativeImageConstructor, inputBytes: Uint8Array): Promise<boolean> {
	try {
		await new NativeImage(inputBytes, { autoOrient: false })
			.resize(1, 1, { filter: "box", fit: "fill" })
			.png()
			.bytes();
		return true;
	} catch {
		return false;
	}
}
