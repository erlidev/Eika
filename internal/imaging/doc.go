// Package imaging prepares the images a user attaches to a message for a
// model. It decodes PNG, JPEG, GIF, and WebP, turns a photo upright by its
// EXIF orientation, fits it within 1920 by 1080 pixels with a Catmull-Rom
// filter, and encodes it as PNG or JPEG, the two formats every endpoint that
// reads images accepts. Metadata such as a photo's location is not kept.
//
// It depends on provider for the Image it returns and on golang.org/x/image
// for the filter and the WebP decoder. Prepare is the entry point.
package imaging
