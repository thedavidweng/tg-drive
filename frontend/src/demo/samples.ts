/**
 * The live demo's drive (ADR 0041): well-known public test media, listed
 * by path, real size, and where the file lives. Nothing here is fetched;
 * the bytes stay on their hosts until a file is opened.
 */
export interface Sample {
  path: string
  size: number
  date: string
  source: string
}

const blender = "https://download.blender.org"

export const samples: Sample[] = [
  {
    path: "/Movies/Big Buck Bunny/big_buck_bunny_720p_surround.mp4",
    size: 61878609,
    date: "2026-09-12T09:14:00Z",
    source: "https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4",
  },
  {
    path: "/Movies/Big Buck Bunny/bbb-splash.png",
    size: 1527631,
    date: "2026-09-12T09:15:00Z",
    source: "https://peach.blender.org/wp-content/uploads/bbb-splash.png",
  },
  {
    path: "/Movies/Big Buck Bunny/title_anouncement.jpg",
    size: 165863,
    date: "2026-09-12T09:15:00Z",
    source: "https://peach.blender.org/wp-content/uploads/title_anouncement.jpg",
  },
  {
    path: "/Movies/Sintel/Sintel.2010.1080p.mkv",
    size: 1180090590,
    date: "2026-09-14T20:02:00Z",
    source: `${blender}/durian/movies/Sintel.2010.1080p.mkv`,
  },
  {
    path: "/Movies/Tears of Steel/tears_of_steel_720p.mov",
    size: 372178639,
    date: "2026-09-18T11:40:00Z",
    source: `${blender}/demo/movies/ToS/tears_of_steel_720p.mov`,
  },
  {
    path: "/Movies/Tears of Steel/TOS-en.srt",
    size: 4764,
    date: "2026-09-18T11:41:00Z",
    source: `${blender}/demo/movies/ToS/subtitles/TOS-en.srt`,
  },
  {
    path: "/Movies/Tears of Steel/copyright.txt",
    size: 610,
    date: "2026-09-18T11:41:00Z",
    source: `${blender}/demo/movies/ToS/copyright.txt`,
  },
  {
    path: "/Movies/Elephants Dream/elephantsdream-720-h264-st-aac.mov",
    size: 152496338,
    date: "2026-09-20T16:25:00Z",
    source: `${blender}/ED/elephantsdream-720-h264-st-aac.mov`,
  },
  {
    path: "/Movies/Elephants Dream/ED_1024.avi",
    size: 445866736,
    date: "2026-09-20T16:31:00Z",
    source: `${blender}/ED/ED_1024.avi`,
  },
  {
    path: "/Movies/Elephants Dream/poster.pdf",
    size: 2162691,
    date: "2026-09-20T16:32:00Z",
    source: `${blender}/ED/poster.pdf`,
  },
  {
    path: "/Movies/Elephants Dream/cover.jpg",
    size: 204925,
    date: "2026-09-20T16:32:00Z",
    source: `${blender}/ED/cover.jpg`,
  },
  {
    path: "/Music/Elephants Dream OST/1-TheWires.mp3",
    size: 1810560,
    date: "2026-09-21T08:05:00Z",
    source: `${blender}/ED/1-TheWires.mp3`,
  },
  {
    path: "/Music/Elephants Dream OST/5-EndTitle.mp3",
    size: 2193536,
    date: "2026-09-21T08:05:00Z",
    source: `${blender}/ED/5-EndTitle.mp3`,
  },
  {
    path: "/Music/Sintel/sintel-m+e-st.flac",
    size: 74266467,
    date: "2026-09-21T08:12:00Z",
    source: `${blender}/durian/movies/sintel-m%2Be-st.flac`,
  },
  {
    path: "/Music/Tears of Steel/TOS_DVDSTEREOMIX.aif",
    size: 211392550,
    date: "2026-09-21T08:20:00Z",
    source: `${blender}/demo/movies/ToS/TOS_DVDSTEREOMIX.aif`,
  },
  {
    path: "/Music/Samples/Example.ogg",
    size: 104793,
    date: "2026-09-22T13:00:00Z",
    source: "https://upload.wikimedia.org/wikipedia/commons/c/c8/Example.ogg",
  },
  {
    path: "/Music/Samples/piano2.wav",
    size: 1210892,
    date: "2026-09-22T13:00:00Z",
    source: "https://www.kozco.com/tech/piano2.wav",
  },
  {
    path: "/Photos/Kodak/kodim01.png",
    size: 736501,
    date: "2026-09-24T18:44:00Z",
    source: "https://r0k.us/graphics/kodak/kodak/kodim01.png",
  },
  {
    path: "/Photos/Kodak/kodim23.png",
    size: 557596,
    date: "2026-09-24T18:44:00Z",
    source: "https://r0k.us/graphics/kodak/kodak/kodim23.png",
  },
  {
    path: "/Photos/Formats/JPEG_example_flower.jpg",
    size: 36287,
    date: "2026-09-25T10:10:00Z",
    source: "https://upload.wikimedia.org/wikipedia/commons/3/3f/JPEG_example_flower.jpg",
  },
  {
    path: "/Photos/Formats/1.webp",
    size: 30320,
    date: "2026-09-25T10:10:00Z",
    source: "https://www.gstatic.com/webp/gallery/1.webp",
  },
  {
    path: "/Photos/Formats/fox.profile0.8bpc.yuv420.avif",
    size: 80743,
    date: "2026-09-25T10:11:00Z",
    source: "https://raw.githubusercontent.com/link-u/avif-sample-images/master/fox.profile0.8bpc.yuv420.avif",
  },
  {
    path: "/Photos/Formats/autumn_1440x960.heic",
    size: 293608,
    date: "2026-09-25T10:11:00Z",
    source: "https://nokiatech.github.io/heif/content/images/autumn_1440x960.heic",
  },
  {
    path: "/Photos/Formats/Rotating_earth_(large).gif",
    size: 1001718,
    date: "2026-09-25T10:12:00Z",
    source: "https://upload.wikimedia.org/wikipedia/commons/2/2c/Rotating_earth_%28large%29.gif",
  },
  {
    path: "/Photos/Formats/SVG_logo.svg",
    size: 4019,
    date: "2026-09-25T10:12:00Z",
    source: "https://upload.wikimedia.org/wikipedia/commons/0/02/SVG_logo.svg",
  },
  {
    path: "/Documents/compressed.tracemonkey-pldi-09.pdf",
    size: 1016315,
    date: "2026-09-27T15:30:00Z",
    source: "https://raw.githubusercontent.com/mozilla/pdf.js/master/web/compressed.tracemonkey-pldi-09.pdf",
  },
  {
    path: "/Documents/dummy.pdf",
    size: 13264,
    date: "2026-09-27T15:30:00Z",
    source: "https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf",
  },
  {
    path: "/Documents/demo.docx",
    size: 1311881,
    date: "2026-09-27T15:31:00Z",
    source: "https://calibre-ebook.com/downloads/demos/demo.docx",
  },
  {
    path: "/Documents/Alice in Wonderland.epub",
    size: 189249,
    date: "2026-09-27T15:32:00Z",
    source: "https://www.gutenberg.org/ebooks/11.epub3.images",
  },
  {
    path: "/Documents/Alice in Wonderland.txt",
    size: 174311,
    date: "2026-09-27T15:32:00Z",
    source: "https://www.gutenberg.org/cache/epub/11/pg11.txt",
  },
  {
    path: "/BigBuckBunny_320x180.mp4.zip",
    size: 64657225,
    date: "2026-09-29T07:58:00Z",
    source: `${blender}/peach/bigbuckbunny_movies/BigBuckBunny_320x180.mp4.zip`,
  },
]
