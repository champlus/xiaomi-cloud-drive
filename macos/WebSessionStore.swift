import Foundation
import WebKit
import Security

private enum WebSessionVault {
    private static let service = "dev.cham.midrive"
    private static let account = "xiaomi-web-session-v2"
    static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }
    private static func read(_ account: String) throws -> Data? {
        var request = query(account)
        request[kSecReturnData as String] = true; request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw failure(status) }
        return data
    }
    static func load(interactive: Bool = false) throws -> [WebSessionCookie]? {
        if let data = try CredentialBroker.run("read") {
            return try JSONDecoder().decode(WebSessionArchive.self, from: data).cookies
        }
        // A background read must return an actionable error, not freeze startup
        // behind a system password dialog. UI is enabled only by the user's button.
        SecKeychainSetUserInteractionAllowed(interactive)
        defer { SecKeychainSetUserInteractionAllowed(false) }
        var cookies: [WebSessionCookie] = []
        let legacyNames: [String]
        if let data = try read(account) {
            if let archive = try? JSONDecoder().decode(WebSessionArchive.self, from: data) { try save(archive.cookies); return archive.cookies }
            // The first development version migrated only the web snapshot.
            // Complete that migration with the service's newer rotated token.
            cookies = try WebSessionCookie.decodeArchive(data)
            legacyNames = ["xiaomi-session"]
        } else { legacyNames = ["xiaomi-web-session", "xiaomi-session"] }
        var found = !cookies.isEmpty
        for legacy in legacyNames {
            if let data = try read(legacy) {
                found = true
                cookies = WebSessionCookie.merging(cookies, with: try WebSessionCookie.decodeArchive(data))
            }
        }
        if found { try save(cookies); return cookies }
        return nil
    }
    static func save(_ cookies: [WebSessionCookie]) throws {
        let data = try JSONEncoder().encode(WebSessionArchive(cookies))
        _ = try CredentialBroker.run("write", data: data)
    }

    static func clear() throws {
        // An empty archive also prevents a future launch from reimporting an old
        // legacy session after the user explicitly signs out.
        try save([])
    }
    static func failure(_ status: OSStatus) -> NSError {
        let message = status == errSecInteractionNotAllowed || status == errSecAuthFailed
            ? "钥匙串需要授权，请点击「恢复登录」"
            : "无法保存或读取登录状态（钥匙串 \(status)）"
        return NSError(domain: "米盘钥匙串", code: Int(status), userInfo: [NSLocalizedDescriptionKey: message])
    }
}

final class WebSessionStore: NSObject, WKHTTPCookieStoreObserver {
    let dataStore = WKWebsiteDataStore.nonPersistent()
    private(set) var ready = false
    private(set) var needsAuthorization = false
    private(set) var hasCredentials = false
    private var applying = false
    private var snapshotRevision: UInt64 = 0
    private var cachedCookies: [WebSessionCookie] = []
    private let vaultQueue = DispatchQueue(label: "dev.cham.midrive.credentials")
    private var restoreInProgress = false
    private var restoreCallbacks: [() -> Void] = []
    var onError: ((String) -> Void)?
    override init() { super.init(); dataStore.httpCookieStore.add(self) }
    private func handle(_ error: Error) {
        let code = (error as NSError).code
        needsAuthorization = code == Int(errSecInteractionNotAllowed) || code == Int(errSecAuthFailed)
        onError?(error.localizedDescription)
    }
    private func updateCredentialState(_ cookies: [WebSessionCookie]) {
        cachedCookies = cookies
        hasCredentials = cookies.contains { ["serviceToken", "passToken", "accountToken"].contains($0.name) && !$0.value.isEmpty && $0.cookie() != nil }
    }
    func restore(completion: @escaping () -> Void) {
        guard !ready else { completion(); return }
        restoreCallbacks.append(completion)
        guard !restoreInProgress else { return }; restoreInProgress = true
        vaultQueue.async {
            let result = Result { try WebSessionVault.load() }
            DispatchQueue.main.async {
                switch result {
                case .success(let saved?):
                    self.updateCredentialState(saved)
                    self.apply(saved.compactMap { $0.cookie() }) { self.finishRestoring() }
                case .failure(let error): self.handle(error); self.finishRestoring()
                case .success(nil): self.restoreLegacyWebStore()
                }
            }
        }
    }
    private func restoreLegacyWebStore() {
        let legacy = WKWebsiteDataStore.default().httpCookieStore
        legacy.getAllCookies { cookies in
            let own = cookies.filter { WebSessionCookie($0).allowed && WebSessionCookie($0).cookie() != nil }
            self.vaultQueue.async {
                let result = Result { try WebSessionVault.save(own.map(WebSessionCookie.init)) }
                DispatchQueue.main.async {
                    switch result {
                    case .success:
                        self.updateCredentialState(own.map(WebSessionCookie.init))
                        self.apply(own) {
                            for cookie in own { legacy.delete(cookie) }
                            self.finishRestoring()
                        }
                    case .failure(let error): self.handle(error); self.finishRestoring()
                    }
                }
            }
        }
    }
    func authorize(completion: @escaping (String?) -> Void) {
        vaultQueue.async {
            let result = Result { try WebSessionVault.load(interactive: true) ?? [] }
            DispatchQueue.main.async {
                switch result {
                case .success(let saved):
                    self.needsAuthorization = false; self.updateCredentialState(saved)
                    self.apply(saved.compactMap { $0.cookie() }) { self.ready = true; completion(nil) }
                case .failure(let error): self.handle(error); completion(error.localizedDescription)
                }
            }
        }
    }

    private func finishRestoring() {
        ready = true; restoreInProgress = false
        let callbacks = restoreCallbacks; restoreCallbacks = []
        for completion in callbacks { completion() }
    }
    func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
        guard ready, !applying else { return }; persist()
    }
    func persist(completion: @escaping () -> Void = {}) {
        guard !applying, !needsAuthorization else { completion(); return }
        snapshotRevision &+= 1; let revision = snapshotRevision
        dataStore.httpCookieStore.getAllCookies { cookies in
            guard revision == self.snapshotRevision, !self.applying else { completion(); return }
            let received = cookies.map(WebSessionCookie.init).filter { $0.allowed && $0.cookie() != nil }
            let saved = WebSessionCookie.merging(self.cachedCookies, with: received)
            self.updateCredentialState(saved)
            self.vaultQueue.async {
                let result = Result { try WebSessionVault.save(saved) }
                DispatchQueue.main.async {
                    if case .failure(let error) = result { self.handle(error) }
                    completion()
                }
            }
        }
    }
    func apply(_ cookies: [HTTPCookie], completion: @escaping () -> Void) {
        applying = true; snapshotRevision &+= 1
        let group = DispatchGroup()
        for cookie in cookies where WebSessionCookie(cookie).allowed {
            group.enter(); dataStore.httpCookieStore.setCookie(cookie) { group.leave() }
        }
        group.notify(queue: .main) { self.applying = false; completion() }
    }
    func serviceCookies(completion: @escaping ([[String: Any]]) -> Void) {
        dataStore.httpCookieStore.getAllCookies { cookies in completion(cookies.compactMap { WebSessionCookie($0).servicePayload }) }
    }
    func clear(completion: @escaping (String?) -> Void) {
        applying = true; snapshotRevision &+= 1
        vaultQueue.async {
            let result = Result { try WebSessionVault.clear() }
            DispatchQueue.main.async {
                if case .failure(let error) = result { self.applying = false; completion(error.localizedDescription); return }
                self.clearWebStore(completion: completion)
            }
        }
    }
    private func clearWebStore(completion: @escaping (String?) -> Void) {
        dataStore.httpCookieStore.getAllCookies { cookies in
            let group = DispatchGroup()
            for cookie in cookies { group.enter(); self.dataStore.httpCookieStore.delete(cookie) { group.leave() } }
            group.notify(queue: .main) { self.applying = false; self.hasCredentials = false; self.cachedCookies = []; self.needsAuthorization = false; completion(nil) }
        }
    }
}
