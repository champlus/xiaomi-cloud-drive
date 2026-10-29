import AppKit
let root = URL(fileURLWithPath: CommandLine.arguments[1])
let app = root.appendingPathComponent("dist/米盘.app")
let info = NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist"))!
let name = info["CFBundleIconFile"] as! String
let image = NSImage(contentsOf: app.appendingPathComponent("Contents/Resources/\(name).icns"))!
let bitmap = NSBitmapImageRep(data: image.tiffRepresentation!)!
let size = bitmap.pixelsWide
var minX = size, maxX = 0, minY = size, maxY = 0
for y in 0..<size {
    for x in 0..<size where bitmap.colorAt(x: x, y: y)!.alphaComponent > 0.1 {
        minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
    }
}
let width = Double(maxX-minX+1)/Double(size), height = Double(maxY-minY+1)/Double(size)
precondition((0.78...0.82).contains(width) && (0.78...0.82).contains(height), "Icon must have consistent clear margins")
precondition(abs(minX-(size-1-maxX)) <= 2 && abs(minY-(size-1-maxY)) <= 2, "Icon must be centered")
let installed = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications/米盘.app/Contents/Resources/\(name).icns")
if FileManager.default.fileExists(atPath: installed.path) {
    let installedData = try Data(contentsOf: installed)
    let builtData = try Data(contentsOf: app.appendingPathComponent("Contents/Resources/\(name).icns"))
    precondition(installedData == builtData)
}
print("Icon footprint, centering and installed asset passed")
