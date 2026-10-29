import Foundation
import dnssd

/// Publishes an alias in this Mac's resolver only; no hosts-file or LAN changes.
final class LocalServiceName {
    static let host = "MiCloud.local"
    static let displayName = "Mi Cloud"
    private var connection: DNSServiceRef?
    private var records: [DNSRecordRef] = []
    static func mountURL(port: Int) -> CFURL {
        let path = "小米云盘".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
        return CFURLCreateWithString(nil,"http://\(host):\(port)/\(path)/" as CFString,nil)!
    }
    private var service: DNSServiceRef?
    func register(port: Int) throws {
        close()
        var status = DNSServiceCreateConnection(&connection)
        guard status == kDNSServiceErr_NoError, let connection = connection else { throw failure(status) }
        let address: [UInt8] = [127, 0, 0, 1]
        for hostname in [Self.host] {
            var record: DNSRecordRef?
            status = address.withUnsafeBytes { bytes in
                DNSServiceRegisterRecord(connection, &record, DNSServiceFlags(kDNSServiceFlagsUnique), kDNSServiceInterfaceIndexLocalOnly, hostname+".", UInt16(kDNSServiceType_A), UInt16(kDNSServiceClass_IN), 4, bytes.baseAddress, 120, { _, _, _, _, _ in }, nil)
            }
            guard status == kDNSServiceErr_NoError, let record = record else { close(); throw failure(status) }
            records.append(record)
        }
        status = DNSServiceSetDispatchQueue(connection, DispatchQueue.main)
        guard status == kDNSServiceErr_NoError else { close(); throw failure(status) }
        status = DNSServiceRegister(&service, 0, kDNSServiceInterfaceIndexLocalOnly, Self.displayName, "_webdav._tcp.", "local.", Self.host+".", UInt16(port).bigEndian, 0, nil, { _, _, _, _, _, _, _ in }, nil)
        if status == kDNSServiceErr_NoError, let service = service { DNSServiceSetDispatchQueue(service, DispatchQueue.main) }
    }
    func close() {
        if let service = service { DNSServiceRefDeallocate(service) }; service = nil
        if let connection = connection { DNSServiceRefDeallocate(connection) }; connection = nil; records.removeAll()
    }
    private func failure(_ code: DNSServiceErrorType) -> NSError { NSError(domain: "米盘", code: Int(code), userInfo: [NSLocalizedDescriptionKey: "无法注册本地云盘名称（\(code)）"]) }
}
