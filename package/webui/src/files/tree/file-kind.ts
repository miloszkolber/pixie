// Match the bounded raster formats served by the authenticated project-file route.
export function isImagePath(path: string): boolean {
	return /\.(?:png|jpe?g|gif|webp)$/i.test(path);
}
