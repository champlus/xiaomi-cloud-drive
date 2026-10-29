import Foundation

struct WebSessionCookie: Codable {
    let name: String
    let value: String
    let domain: String
    let path: String
    let expires: Date?
    let secure: Bool
    let httpOnly: Bool

    init(_ cookie: HTTPCookie) {
        name = cookie.name; value = cookie.value; domain = cookie.domain
        path = cookie.path; expires = cookie.expiresDate
        secure = cookie.isSecure; httpOnly = cookie.isHTTPOnly
    }
    var allowed: Bool {
        ["mi.com", "i.mi.com", "account.xiaomi.com", "account.xiaomi.cn", "xiaomi.com", "xiaomi.cn"]
            .contains(domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased())
    }
    func cookie(at now: Date = Date()) -> HTTPCookie? {
        guard allowed, expires.map({ $0 > now }) ?? true else { return nil }
        var properties: [HTTPCookiePropertyKey: Any] = [.name: name, .value: value, .domain: domain, .path: path]
        if secure { properties[.secure] = "TRUE" }
        if httpOnly { properties[HTTPCookiePropertyKey("HttpOnly")] = "TRUE" }
        if let expiry = expires { properties[.expires] = expiry }
        return HTTPCookie(properties: properties)
    }
    var servicePayload: [String: Any]? {
        let scope = domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased()
        let names: Set<String> = ["serviceToken", "userId", "i.mi.com_ph", "i.mi.com_slh", "i.mi.com_isvalid_servicetoken", "i.mi.com_istrudev", "deviceId", "uLocale"]
        guard ["mi.com", "i.mi.com"].contains(scope), names.contains(name), cookie() != nil else { return nil }
        var payload: [String: Any] = ["Name": name, "Value": value, "Domain": domain, "Path": path, "Secure": secure, "HttpOnly": httpOnly]
        if let expiry = expires { payload["Expires"] = ISO8601DateFormatter().string(from: expiry) }
        return payload
    }
    static func merging(_ saved: [WebSessionCookie], with received: [WebSessionCookie]) -> [WebSessionCookie] {
        let incoming = received.filter { $0.allowed && $0.cookie() != nil }
        let previous = saved.filter { $0.allowed && $0.cookie() != nil }
        let oldAccount = previous.first { $0.name == "userId" }?.value
        let newAccount = incoming.first { $0.name == "userId" }?.value
        if let old = oldAccount, let new = newAccount, old != new { return incoming }
        var merged: [String: WebSessionCookie] = [:]
        for cookie in previous + incoming {
            let key = cookie.domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")) + "\n" + cookie.path + "\n" + cookie.name
            merged[key] = cookie
        }
        return merged.keys.sorted().compactMap { merged[$0] }
    }
    static func decodeArchive(_ data: Data) throws -> [WebSessionCookie] {
        if let archive = try? JSONDecoder().decode(WebSessionArchive.self, from: data) { return archive.cookies }
        if let saved = try? JSONDecoder().decode([WebSessionCookie].self, from: data) { return saved }
        let payload = try JSONSerialization.jsonObject(with: data) as? [[String: Any]] ?? []
        return payload.compactMap(fromService).map(WebSessionCookie.init)
    }
    static func fromService(_ payload: [String: Any]) -> HTTPCookie? {
        guard let name = payload["Name"] as? String, let value = payload["Value"] as? String,
              let domain = payload["Domain"] as? String else { return nil }
        var properties: [HTTPCookiePropertyKey: Any] = [.name: name, .value: value, .domain: domain, .path: payload["Path"] as? String ?? "/", .secure: "TRUE"]
        let formatter = ISO8601DateFormatter(); formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let text = payload["Expires"] as? String, let date = formatter.date(from: text) ?? ISO8601DateFormatter().date(from: text), date.timeIntervalSince1970 > 0 {
            guard date > Date() else { return nil }; properties[.expires] = date
        }
        if payload["HttpOnly"] as? Bool == true { properties[HTTPCookiePropertyKey("HttpOnly")] = "TRUE" }
        guard let cookie = HTTPCookie(properties: properties), WebSessionCookie(cookie).servicePayload != nil else { return nil }
        return cookie
    }
}

enum LoginRefreshResult { case connected, loginRequired, transientFailure }
struct LoginRefreshPolicy {
    private(set) var nextAttempt = Date.distantPast
    private(set) var deadline: Date?
    private var failures = 0
    mutating func begin(at now: Date = Date()) -> Bool {
        guard deadline == nil, now >= nextAttempt else { return false }
        deadline = now.addingTimeInterval(60); return true
    }
    mutating func finish(at now: Date = Date(), result: LoginRefreshResult) {
        deadline = nil
        switch result {
        case .connected: failures = 0; nextAttempt = now.addingTimeInterval(20 * 60)
        case .loginRequired: failures = 0; nextAttempt = now.addingTimeInterval(5 * 60)
        case .transientFailure:
            failures = min(failures + 1, 5)
            nextAttempt = now.addingTimeInterval(min(60 * pow(2, Double(failures - 1)), 15 * 60))
        }
    }
    mutating func request(at now: Date = Date()) { if deadline == nil { nextAttempt = now } }
    func expired(at now: Date = Date()) -> Bool { deadline.map { now >= $0 } ?? false }
}

struct WebSessionArchive: Codable {
    let version: Int
    let cookies: [WebSessionCookie]
    init(_ cookies: [WebSessionCookie]) { version = 2; self.cookies = cookies }
}

struct ConnectionPresentation {
    static func label(connected: Bool, checking: Bool, renewing: Bool, keychainRequired: Bool, authRequired: Bool, hasSession: Bool) -> String {
        if keychainRequired { return "需要恢复" }
        if renewing { return "恢复连接中" }
        if checking { return "连接中" }
        if connected { return "已连接" }
        if authRequired { return "需要验证" }
        return hasSession ? "连接中断" : "未登录"
    }
}
