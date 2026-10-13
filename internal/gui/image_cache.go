package gui

import "image"

// ImageOp retains RGBA pixels even when its source used a palette.
func imageBytes(img image.Image) int64 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	return int64(b.Dx()) * int64(b.Dy()) * 4
}

// trimCacheLocked releases the least recently used completed entries.
// Loading entries stay registered until their worker completes. The caller
// keeps the entry it is about to draw, avoiding eviction between lookup and use.
func (im *images) trimCacheLocked(protect *imageEntry) {
	for im.cacheBytes > imageCacheBudget || len(im.entries) > imageCacheEntries {
		var oldestKey string
		var oldest *imageEntry
		for key, entry := range im.entries {
			if entry == protect || entry.state == imageLoading {
				continue
			}
			if oldest == nil || entry.used < oldest.used {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			return
		}
		im.cacheBytes -= oldest.bytes
		delete(im.entries, oldestKey)
	}
}
