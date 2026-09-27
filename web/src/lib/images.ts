/** imageUrl is base64 image data of a media type as a URL an img element loads. */
export function imageUrl(mediaType: string, data: string): string {
  return `data:${mediaType};base64,${data}`;
}
