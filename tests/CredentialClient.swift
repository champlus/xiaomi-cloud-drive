import Foundation
@main struct Client {
    static func main() {
        do {
            let value = try CredentialBroker.run(CommandLine.arguments[2], data: CommandLine.arguments[2] == "write" ? Data("synthetic".utf8) : nil, executable: URL(fileURLWithPath: CommandLine.arguments[1]))
            if CommandLine.arguments[2] == "read", value != Data("synthetic".utf8) { exit(95) }
        } catch { exit(96) }
    }
}
