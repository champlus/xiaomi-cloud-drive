import AppKit
func sample(_ image: NSImage) -> NSBitmapImageRep {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 128, pixelsHigh: 128, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState(); NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    image.draw(in: NSRect(x: 0, y: 0, width: 128, height: 128), from: .zero, operation: .copy, fraction: 1)
    NSGraphicsContext.restoreGraphicsState(); return bitmap
}
let cached = sample(NSImage(contentsOfFile: CommandLine.arguments[1])!)
let installed = sample(NSImage(contentsOfFile: CommandLine.arguments[2])!)
var difference = 0.0
for y in 0..<128 {
    for x in 0..<128 {
        let a = cached.colorAt(x: x, y: y)!.usingColorSpace(.sRGB)!, b = installed.colorAt(x: x, y: y)!.usingColorSpace(.sRGB)!
        difference += abs(a.alphaComponent*a.redComponent-b.alphaComponent*b.redComponent)
        difference += abs(a.alphaComponent*a.greenComponent-b.alphaComponent*b.greenComponent)
        difference += abs(a.alphaComponent*a.blueComponent-b.alphaComponent*b.blueComponent)
        difference += abs(a.alphaComponent-b.alphaComponent)
    }
}
let error = difference / Double(128*128*4)
guard error < 0.06 else { print(String(format: "FAIL: Launchpad icon differs from installed asset (%.3f)", error)); exit(1) }
print(String(format: "PASS: Launchpad icon matches installed asset (%.3f)", error))
