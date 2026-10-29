import Foundation

@main struct SessionTests {
    static func main() throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let expiry = now.addingTimeInterval(60 * 24 * 3600)
        let original = HTTPCookie(properties: [.name: "passToken", .value: "synthetic-account-session", .domain: ".account.xiaomi.com", .path: "/", .secure: "TRUE", .expires: expiry])!
        let saved = WebSessionCookie(original)
        let encoded = try JSONEncoder().encode(saved)
        let restored = try JSONDecoder().decode(WebSessionCookie.self, from: encoded).cookie(at: now)!
        precondition(restored.expiresDate == expiry, "restoring login must retain the server's two-month validity")
        precondition(saved.cookie(at: expiry.addingTimeInterval(1)) == nil, "expired credentials must not be extended locally")
        let sessionOnly = HTTPCookie(properties: [.name: "passToken", .value: "synthetic-session-only", .domain: ".account.xiaomi.com", .path: "/"])!
        precondition(WebSessionCookie(sessionOnly).cookie(at: now) != nil, "account session must survive app restart through Keychain")
        let foreign = HTTPCookie(properties: [.name: "passToken", .value: "synthetic-foreign", .domain: "account.xiaomi.com.evil.example", .path: "/"])!
        precondition(!WebSessionCookie(foreign).allowed, "unrelated website credentials must not be retained")
        let nested = HTTPCookie(properties: [.name: "serviceToken", .value: "synthetic-drive", .domain: ".i.mi.com", .path: "/drive", .secure: "TRUE", .expires: expiry])!
        let drive = WebSessionCookie(nested).servicePayload!
        precondition(drive["Path"] as? String == "/drive" && drive["Expires"] != nil, "native-to-service import must preserve cookie scope and lifetime")
        precondition(saved.servicePayload == nil, "account renewal credentials must never be sent to the cloud API client")

        let fractionalExpiry = "2028-01-01T00:00:00.123Z"
        let restoredFromService = WebSessionCookie.fromService(["Name": "serviceToken", "Value": "synthetic", "Domain": ".i.mi.com", "Path": "/", "Expires": fractionalExpiry])!
        precondition(restoredFromService.expiresDate != nil, "service timestamps with fractional seconds must retain their expiry")

        let legacyArchive = Data("[{\"Name\":\"serviceToken\",\"Value\":\"synthetic-legacy\",\"Domain\":\".i.mi.com\",\"Path\":\"/\",\"Secure\":true}]".utf8)
        let migrated = try WebSessionCookie.decodeArchive(legacyArchive)
        precondition(migrated.count == 1 && migrated[0].name == "serviceToken", "legacy Go-owned sessions must migrate without login")
        let cleared = try WebSessionCookie.decodeArchive(Data("[]".utf8))
        precondition(cleared.isEmpty, "explicit logout archive must remain empty")

        let backup = [WebSessionCookie(original)]
        precondition(WebSessionCookie.merging(backup, with: []).count == 1, "an empty background cookie snapshot must not erase saved login")
        let driveOnly = [WebSessionCookie(nested)]
        precondition(WebSessionCookie.merging(backup, with: driveOnly).count == 2, "a service-token refresh must retain account renewal credentials")

        var policy = LoginRefreshPolicy()
        precondition(policy.begin(at: now))
        precondition(!policy.begin(at: now.addingTimeInterval(1)), "only one renewal may run")
        policy.finish(at: now, result: .transientFailure)
        precondition(!policy.begin(at: now.addingTimeInterval(5)), "failed renewal must back off")
        precondition(policy.begin(at: now.addingTimeInterval(61)), "failed hidden WebView must not block all future renewals")
        precondition(policy.expired(at: now.addingTimeInterval(122)), "stalled navigation must have a deadline")
        policy.finish(at: now.addingTimeInterval(122), result: .connected)
        precondition(!policy.begin(at: now.addingTimeInterval(123)), "valid sessions must not rotate on every heartbeat")
        policy.request(at: now.addingTimeInterval(124))
        precondition(policy.begin(at: now.addingTimeInterval(124)), "a rejected request must trigger immediate renewal")
        print("PASS: account-session persistence, scope, expiry, renewal backoff and stalled-navigation recovery")
    }
}
