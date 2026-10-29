import Foundation
import Security
import CryptoKit
import Darwin

// A separate, byte-stable Keychain owner survives changes to the UI executable.
enum CredentialBroker {
    static func run(_ operation: String, data: Data? = nil, executable: URL? = nil) throws -> Data? {
        let process = Process()
        process.executableURL = executable ?? Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/credential-helper")
        process.arguments = [operation]
        let input = Pipe(), output = Pipe()
        process.standardInput = input; process.standardOutput = output; process.standardError = FileHandle.nullDevice
        try process.run()
        let watchdog = DispatchWorkItem { if process.isRunning { kill(process.processIdentifier, SIGKILL) } }
        DispatchQueue.global().asyncAfter(deadline: .now() + 8, execute: watchdog)
        defer { watchdog.cancel(); try? input.fileHandleForWriting.close(); try? output.fileHandleForReading.close() }
        do {
            var ready = Data()
            while ready.count < 9 {
                guard let chunk = try output.fileHandleForReading.read(upToCount: 9-ready.count), !chunk.isEmpty else { break }
                ready.append(chunk)
            }
            guard ready == Data("MPVAULT1\n".utf8), trustedChild(process.processIdentifier) else {
                if process.isRunning { kill(process.processIdentifier, SIGKILL) }; process.waitUntilExit()
                throw failure(77)
            }
            // No archive crosses the pipe until the running helper has been verified.
            try input.fileHandleForWriting.write(contentsOf: Data("GO\n".utf8))
            if let data = data { try input.fileHandleForWriting.write(contentsOf: data) }
            try input.fileHandleForWriting.close()
            let result = try output.fileHandleForReading.readToEnd() ?? Data()
            process.waitUntilExit()
            if process.terminationStatus == 44 { return nil }
            guard process.terminationReason == .exit, process.terminationStatus == 0 else { throw failure(process.terminationStatus) }
            return result
        } catch {
            if process.isRunning { process.terminate(); process.waitUntilExit() }
            throw error
        }
    }
    private static func trustedChild(_ pid: pid_t) -> Bool {
        var own: SecCode?, staticOwn: SecStaticCode?, info: CFDictionary?, child: SecCode?, requirement: SecRequirement?
        guard SecCodeCopySelf([], &own) == errSecSuccess, let own = own,
              SecCodeCopyStaticCode(own, [], &staticOwn) == errSecSuccess, let staticOwn = staticOwn,
              SecCodeCopySigningInformation(staticOwn, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
              let certificates = (info as? [String: Any])?[kSecCodeInfoCertificates as String] as? [SecCertificate],
              let certificate = certificates.first else { return false }
        let fingerprint = Insecure.SHA1.hash(data: SecCertificateCopyData(certificate) as Data).map { String(format: "%02x", $0) }.joined()
        let expression = "identifier \"dev.cham.midrive.credentials\" and certificate leaf = H\"\(fingerprint)\""
        guard SecRequirementCreateWithString(expression as CFString, [], &requirement) == errSecSuccess,
              SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributePid as String: pid] as CFDictionary, [], &child) == errSecSuccess,
              let child = child, let requirement = requirement else { return false }
        return SecCodeCheckValidity(child, SecCSFlags(rawValue: kSecCSStrictValidate), requirement) == errSecSuccess
    }
    private static func failure(_ code: Int32) -> NSError {
        if code == 70 { return NSError(domain: "米盘钥匙串", code: Int(errSecAuthFailed), userInfo: [NSLocalizedDescriptionKey: "钥匙串需要授权，请点击「恢复登录」"]) }
        return NSError(domain: "米盘凭证存储", code: Int(code), userInfo: [NSLocalizedDescriptionKey: "登录状态存储暂不可用（\(code)）"])
    }
}
