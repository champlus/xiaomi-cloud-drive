import Foundation
@main struct TransferTests {
    static func main() throws {
        precondition(FileTransfer.byteCount(374144) == "374 KB", "file sizes should match Finder decimal units")
        let record: [String: Any] = ["id": "fixture", "path": "/资料/照片.jpg", "direction": "upload", "state": "active", "phase": "uploading", "bytes": 1024, "total": 2048, "speed": 1536, "startedAt": "2026-10-29T16:00:00.123456789+08:00", "partial": false]
        let snapshot = try TransferSnapshot.decode(["active": [record], "completed": [], "failed": []])
        precondition(snapshot.active.count == 1 && snapshot.completed.isEmpty)
        let file = snapshot.active[0]
        precondition(file.name == "照片.jpg" && file.folder == "/资料")
        precondition(file.progress == 0.5 && file.speedLabel == "1.5 KB/s")
        precondition(file.progressLabel == "1.0 KB / 2.0 KB" && file.phaseLabel == "上传中")
        var unknown = record; unknown["total"] = -1
        let unknownSnapshot = try TransferSnapshot.decode(["active": [unknown], "completed": [], "failed": []])
        precondition(unknownSnapshot.active[0].progress == nil)
        precondition(TransferSafety.hasActive(["transfers": ["active": [["direction": "download"]]], "pending": []]), "quitting must not interrupt active downloads")
        precondition(TransferSafety.hasActive(["pending": [["state": "uploading"]]]), "legacy uploads remain protected")
        precondition(!TransferSafety.hasActive(["transfers": ["active": [], "completed": [record]], "pending": []]))
        print("PASS: native transfer progress, dates, speed and quit protection")
    }
}
