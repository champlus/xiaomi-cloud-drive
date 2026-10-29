import Foundation
import Darwin

@main struct NativeTests {
    static func main() throws {
        let name = LocalServiceName()
        try name.register(port: 54321)
        let url=LocalServiceName.mountURL(port:54321)
        precondition((CFURLCopyHostName(url) as String?) == "MiCloud.local", "Finder must receive the readable service name")
        var hints = addrinfo(); hints.ai_family = AF_INET; hints.ai_socktype = SOCK_STREAM
        var result: UnsafeMutablePointer<addrinfo>?
        let status = getaddrinfo(LocalServiceName.host, nil, &hints, &result)
        guard status == 0, let address = result else { fatalError("named local service did not resolve: \(status)") }
        defer { freeaddrinfo(address) }
        let v4 = UnsafeRawPointer(address.pointee.ai_addr).assumingMemoryBound(to: sockaddr_in.self)
        let ip = v4.pointee.sin_addr
        let text = String(cString: inet_ntoa(ip))
        precondition(text == "127.0.0.1", "local service must resolve only to loopback")
        print("PASS: MiCloud.local resolves to the local service without hosts-file changes")
        name.close()
    }
}
