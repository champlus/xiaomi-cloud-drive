import Foundation

struct FileTransfer: Decodable, Identifiable, Equatable {
    let id: String
    let path: String
    let direction: String
    let state: String
    let phase: String
    let bytes: Int64
    let total: Int64
    let speed: Double
    let startedAt: Date
    let finishedAt: Date?
    let error: String?
    let partial: Bool
    var name: String { (path as NSString).lastPathComponent }
    var folder: String { (path as NSString).deletingLastPathComponent }
    var active: Bool { state == "active" }
    var completed: Bool { state == "completed" }
    var measurable: Bool { ["receiving", "uploading", "downloading"].contains(phase) }
    var progress: Double? { total > 0 ? min(1, max(0, Double(bytes) / Double(total))) : nil }
    var speedLabel: String { Self.byteCount(speed) + "/s" }
    var sizeLabel: String { Self.byteCount(Double(max(total, bytes))) }
    var progressLabel: String {
        let received = Self.byteCount(Double(bytes))
        return total >= 0 ? received + " / " + Self.byteCount(Double(total)) : received
    }
    var directionLabel: String { direction == "upload" ? "上传" : partial ? "部分下载" : "下载" }
    var phaseLabel: String {
        if state == "interrupted" { return "已中断" }
        if state == "failed" { return "传输失败" }
        switch phase {
        case "receiving": return "接收文件"
        case "preparing": return "准备上传"
        case "uploading": return "上传中"
        case "downloading": return "下载中"
        case "committing": return "正在保存"
        case "completed": return "已完成"
        default: return "等待传输"
        }
    }
    var failureLabel: String {
        switch error ?? "" {
        case "unexpected EOF": return "文件未完整传输，请重试"
        case "context canceled": return "传输已中断"
        case "context deadline exceeded": return "传输超时，请重试"
        default: return error ?? "传输未完成"
        }
    }
    static func byteCount(_ value: Double) -> String {
        var size = value.isFinite ? max(0, value) : 0
        let units = ["B", "KB", "MB", "GB", "TB"]
        var index = 0
        while size >= 1000 && index < units.count - 1 { size /= 1000; index += 1 }
        return String(format: index == 0 || size >= 100 ? "%.0f %@" : "%.1f %@", size, units[index])
    }
}
struct TransferSnapshot: Decodable, Equatable {
    var active: [FileTransfer] = []
    var completed: [FileTransfer] = []
    var failed: [FileTransfer] = []
    var error: String?
    static func decode(_ value: Any?) throws -> TransferSnapshot {
        guard let value = value else { return TransferSnapshot() }
        let decoder = JSONDecoder()
        let fractional = ISO8601DateFormatter(); fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let standard = ISO8601DateFormatter()
        decoder.dateDecodingStrategy = .custom { decoder in
            let value = try decoder.singleValueContainer().decode(String.self)
            guard let date = fractional.date(from: value) ?? standard.date(from: value) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Invalid transfer time"))
            }
            return date
        }
        return try decoder.decode(TransferSnapshot.self, from: JSONSerialization.data(withJSONObject: value))
    }
}
enum TransferSafety {
    static func hasActive(_ state: [String: Any]) -> Bool {
        let transfers = state["transfers"] as? [String: Any] ?? [:]
        if let active = transfers["active"] as? [Any], !active.isEmpty { return true }
        return (state["pending"] as? [[String: Any]] ?? []).contains { ["receiving", "uploading"].contains($0["state"] as? String ?? "") }
    }
}
