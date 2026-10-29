import AppKit
import WebKit
import SwiftUI
import NetFS
import Darwin

struct Runtime: Decodable { let port: Int; let token: String; let password: String; let pid: Int32 }

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, NSWindowDelegate {
    let support = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/米盘", isDirectory: true)
    let mountPoint = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("小米云盘", isDirectory: true)
    let launchLabel = "dev.cham.midrive"
    let localName = LocalServiceName()
    var runtime: Runtime?
    var service: Process?
    var statusItem: NSStatusItem!
    var settingsWindow: NSWindow?
    lazy var model = SettingsModel(app: self)
    var loginWindow: NSWindow?
    var loginWeb: WKWebView?
    var timer: Timer?
    var mounting = false
    var stopping = false
    var autoMount = true
    var nextMountAttempt = Date.distantPast
    var importingSession = false
    var connected = false
    var connectionLabel = "连接中"
    var serviceReadyAttempts = 0
    lazy var webSession = WebSessionStore()
    var refreshPolicy = LoginRefreshPolicy()
    var loginTimeout: DispatchWorkItem?
    var serverRenewalRequested = false
    var restoredServiceSession = false
    var syncingServiceCookies = false
    var savedSessionVersion: UInt64?
    var mountedPath: String?
    var background: Bool { CommandLine.arguments.contains("--background") }
    var startupURL: URL { FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/LaunchAgents/\(launchLabel).plist") }
    var startup: Bool { FileManager.default.fileExists(atPath: startupURL.path) }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        if let iconName = Bundle.main.object(forInfoDictionaryKey: "CFBundleIconFile") as? String, let iconURL = Bundle.main.url(forResource: iconName, withExtension: "icns"), let icon = NSImage(contentsOf: iconURL) { NSApp.applicationIconImage = icon }
        if NSRunningApplication.runningApplications(withBundleIdentifier: "dev.cham.midrive").contains(where: { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }) { exit(EXIT_SUCCESS) }
        configureMainMenu()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        statusItem.button?.image = NSImage(systemSymbolName: "externaldrive.badge.icloud", accessibilityDescription: "米盘")
        buildMenu()
        NSWorkspace.shared.notificationCenter.addObserver(self, selector: #selector(woke), name: NSWorkspace.didWakeNotification, object: nil)
        do { try FileManager.default.createDirectory(at: support, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700]); try startService(); if !UserDefaults.standard.bool(forKey: "hasChosenStartup") { try setStartup(true) } } catch { showError(error.localizedDescription) }
        webSession.onError = { [weak self] message in self?.model.error = message }
        webSession.restore { [weak self] in self?.heartbeat() }
        timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in self?.heartbeat() }
    }
    func configureMainMenu() {
        let menu = NSMenu()
        let application = NSMenuItem(); menu.addItem(application)
        let appMenu = NSMenu(); application.submenu = appMenu
        let about = NSMenuItem(title: "关于米盘", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: ""); appMenu.addItem(about)
        appMenu.addItem(.separator())
        let preferences = NSMenuItem(title: "设置…", action: #selector(showSettings), keyEquivalent: ","); preferences.target = self; appMenu.addItem(preferences)
        appMenu.addItem(.separator())
        let exitItem = NSMenuItem(title: "退出米盘", action: #selector(quit), keyEquivalent: "q"); exitItem.target = self; appMenu.addItem(exitItem)
        let edit = NSMenuItem(title: "编辑", action: nil, keyEquivalent: ""); menu.addItem(edit)
        let editMenu = NSMenu(title: "编辑"); edit.submenu = editMenu
        for (title, action, key) in [("撤销", "undo:", "z"), ("剪切", "cut:", "x"), ("复制", "copy:", "c"), ("粘贴", "paste:", "v"), ("全选", "selectAll:", "a")] {
            editMenu.addItem(NSMenuItem(title: title, action: Selector(action), keyEquivalent: key))
        }
        NSApp.mainMenu = menu
    }
    func buildMenu() {
        let menu = NSMenu()
        let state = NSMenuItem(title: "小米云盘 · \(connectionLabel)", action: nil, keyEquivalent: ""); state.isEnabled = false; menu.addItem(state)
        menu.addItem(NSMenuItem(title: "在 Finder 中打开", action: #selector(openFinder), keyEquivalent: "o"))
        menu.addItem(NSMenuItem(title: "设置…", action: #selector(showSettings), keyEquivalent: ","))
        menu.addItem(NSMenuItem(title: "弹出云盘", action: #selector(eject), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "退出米盘", action: #selector(quit), keyEquivalent: "q"))
        for item in menu.items { item.target = self }; statusItem.menu = menu
    }
    func startService() throws {
        let process = Process(); process.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/midrive-service")
        process.arguments = ["--data-dir", support.path, "--native-session"]
        let logURL = support.appendingPathComponent("service.log")
        if let size = try? logURL.resourceValues(forKeys: [.fileSizeKey]).fileSize, size > 1_000_000 { try? FileManager.default.removeItem(at: logURL) }
        FileManager.default.createFile(atPath: logURL.path, contents: nil, attributes: [.posixPermissions: 0o600])
        let log = try FileHandle(forWritingTo: logURL); try log.seekToEnd(); process.standardOutput = log; process.standardError = log
        process.terminationHandler = { [weak self] _ in DispatchQueue.main.async { guard let self = self, !self.stopping else { return }; self.runtime = nil; self.connected = false; self.buildMenu(); DispatchQueue.main.asyncAfter(deadline: .now()+3) { if !self.stopping { try? self.startService() } } } }
        try process.run(); service = process; serviceReadyAttempts = 0; waitForService()
    }
    func waitForService() {
        serviceReadyAttempts += 1
        if let bytes = try? Data(contentsOf: support.appendingPathComponent("runtime.json")), let r = try? JSONDecoder().decode(Runtime.self, from: bytes), r.pid == service?.processIdentifier { runtime = r; do { try localName.register(port: r.port) } catch { showError(error.localizedDescription) }; if !background { showSettings() }; heartbeat(); return }
        if serviceReadyAttempts < 100 { DispatchQueue.main.asyncAfter(deadline: .now()+0.1) { [weak self] in self?.waitForService() } }
        else { showError("本地服务未能启动。可在 ~/Library/Application Support/米盘/service.log 查看原因。") }
    }
    func request(_ endpoint: String, body: Any? = nil, completion: @escaping (Result<[String: Any], Error>) -> Void) {
        guard let r = runtime, let url = URL(string: "http://127.0.0.1:\(r.port)/api/\(endpoint)") else { completion(.failure(problem("本地服务尚未就绪"))); return }
        var req = URLRequest(url: url); req.timeoutInterval = 60; req.setValue("Bearer \(r.token)", forHTTPHeaderField: "Authorization")
        if let body = body { req.httpMethod = "POST"; req.setValue("application/json", forHTTPHeaderField: "Content-Type"); req.httpBody = try? JSONSerialization.data(withJSONObject: body) }
        URLSession.shared.dataTask(with: req) { data, response, error in DispatchQueue.main.async {
            if let error = error { completion(.failure(error)); return }
            guard let response = response as? HTTPURLResponse, let data = data else { completion(.failure(self.problem("本地服务未响应"))); return }
            if response.statusCode != 200 { completion(.failure(NSError(domain: "米盘", code: response.statusCode, userInfo: [NSLocalizedDescriptionKey: String(data: data, encoding: .utf8) ?? "操作失败"]))); return }
            completion(.success((try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]))
        } }.resume()
    }
    func problem(_ text: String) -> NSError { NSError(domain: "米盘", code: 1, userInfo: [NSLocalizedDescriptionKey: text]) }
    func heartbeat() {
        guard runtime != nil, !stopping else { return }
        request("status") { [weak self] result in guard let self = self else { return }; if case .success(let state) = result {
            self.connected = state["connected"] as? Bool ?? false
            self.autoMount = (state["config"] as? [String: Any])?["autoMount"] as? Bool ?? true
            self.connectionLabel = ConnectionPresentation.label(connected: self.connected, checking: state["checking"] as? Bool ?? false, renewing: state["renewing"] as? Bool ?? false, keychainRequired: self.webSession.needsAuthorization, authRequired: state["authRequired"] as? Bool ?? false, hasSession: state["hasSession"] as? Bool ?? false)
            self.buildMenu()
            if self.connected && self.autoMount && !self.isMounted() && Date() > self.nextMountAttempt { self.mount(open: false) { _ in } }
            let renewing = state["renewing"] as? Bool ?? false
            if renewing && !self.serverRenewalRequested { self.refreshPolicy.request(); self.serverRenewalRequested = true }
            if !renewing { self.serverRenewalRequested = false }
            if self.refreshPolicy.expired(), let web = self.loginWeb, self.loginWindow == nil { self.finishLoginRefresh(web, result: .transientFailure) }
            if !self.webSession.needsAuthorization {
                if state["hasSession"] as? Bool != true, self.webSession.hasCredentials, !self.restoredServiceSession { self.restoreServiceSession() }
                if self.connected && !renewing { self.persistServiceSession(state) }
                if state["hasSession"] as? Bool == true || renewing { self.refreshLogin() }
            }
        } }
    }
    @objc func woke() { nextMountAttempt = Date().addingTimeInterval(5); DispatchQueue.main.asyncAfter(deadline: .now()+6) { self.request("check", body: [:]) { _ in }; self.heartbeat() } }
    func isMounted() -> Bool {
        var pointer: UnsafeMutablePointer<statfs>?; let count = getmntinfo(&pointer, MNT_NOWAIT)
        guard let first = pointer, count > 0 else { return false }
        for i in 0..<Int(count) { var entry = first[i]; let name = withUnsafePointer(to: &entry.f_mntonname) { $0.withMemoryRebound(to: CChar.self, capacity: Int(MAXPATHLEN)) { String(cString: $0) } }; if name == mountPoint.path { mountedPath = name; return true } }
        mountedPath = nil; return false
    }
    func mount(open: Bool, completion: @escaping (String?) -> Void) {
        if isMounted() { if open { NSWorkspace.shared.open(mountPoint) }; completion(nil); return }
        guard !mounting else { completion("正在挂载，请稍候"); return }
        guard let r = runtime else { completion("服务尚未就绪"); return }
        mounting = true; nextMountAttempt = Date().addingTimeInterval(60)
        do { try FileManager.default.createDirectory(at: mountPoint, withIntermediateDirectories: true) } catch { mounting = false; completion(error.localizedDescription); return }
        let mountURL = mountPoint
        DispatchQueue.global(qos: .userInitiated).async {
            let options = NSMutableDictionary(dictionary: ["UIOption": "NoUI", "ServerDisplayName": LocalServiceName.displayName])
            let mountOptions = NSMutableDictionary(dictionary: ["MountAtMountDir": true, "DisplayName": LocalServiceName.displayName])
            var points: Unmanaged<CFArray>?
            let result = NetFSMountURLSync(LocalServiceName.mountURL(port: r.port), mountURL as CFURL, "xiaomi" as CFString, r.password as CFString, options, mountOptions, &points)
            _ = points?.takeRetainedValue()
            DispatchQueue.main.async {
                self.mounting = false
                if result == 0 || self.isMounted() { self.mountedPath = mountURL.path; if open { NSWorkspace.shared.open(mountURL) }; self.notifyState(); completion(nil) }
                else { completion("Finder 挂载失败（\(result)）。请检查连接后重试。") }
            }
        }
    }
    func unmount(completion: @escaping (String?) -> Void) {
        guard isMounted() else { completion(nil); return }
        let target = mountPoint
        DispatchQueue.global(qos: .userInitiated).async {
            let process = Process(); process.executableURL = URL(fileURLWithPath: "/sbin/umount"); process.arguments = [target.path]; process.standardOutput = FileHandle.nullDevice; process.standardError = FileHandle.nullDevice
            do { try process.run(); process.waitUntilExit(); DispatchQueue.main.async { if process.terminationStatus == 0 { self.mountedPath = nil; self.nextMountAttempt = Date().addingTimeInterval(3600); self.notifyState(); completion(nil) } else { completion("云盘正被使用。请关闭正在访问云盘的文件或 Finder 窗口后重试。") } } } catch { DispatchQueue.main.async { completion(error.localizedDescription) } }
        }
    }
    @objc func openFinder() { mount(open: true) { error in if let error = error { self.showError(error) } } }
    @objc func eject() { unmount { error in if let error = error { self.showError(error) } } }
    @objc func showSettings() {
        guard runtime != nil else { return }
        if let window = settingsWindow { model.refresh(); window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 760, height: 520), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "米盘"; window.minSize = NSSize(width: 700, height: 460); window.center(); window.isReleasedWhenClosed = false
        let hosting = NSHostingView(rootView: SettingsView(model: model)); window.contentView = hosting
        settingsWindow = window; window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func authorizeKeychain(completion: @escaping (String?) -> Void) {
        webSession.authorize { error in
            if error == nil { self.restoredServiceSession = false; self.savedSessionVersion = nil; self.heartbeat() }
            completion(error)
        }
    }
    func restoreServiceSession() {
        guard webSession.ready, !webSession.needsAuthorization, loginWeb == nil else { return }
        restoredServiceSession = true
        webSession.serviceCookies { cookies in
            guard cookies.contains(where: { $0["Name"] as? String == "serviceToken" }) else { self.refreshPolicy.request(); self.refreshLogin(); return }
            self.request("session", body: ["cookies": cookies, "background": true]) { result in
                switch result {
                case .success: self.refreshPolicy.finish(result: .connected); self.heartbeat(); self.model.refresh()
                case .failure: self.refreshPolicy.request(); self.refreshLogin()
                }
            }
        }
    }
    func persistServiceSession(_ state: [String: Any]) {
        guard webSession.ready, !webSession.needsAuthorization, !syncingServiceCookies, loginWeb == nil,
              let version = (state["sessionVersion"] as? NSNumber)?.uint64Value, version != savedSessionVersion else { return }
        syncingServiceCookies = true
        request("session/cookies") { result in
            guard self.loginWeb == nil else { self.syncingServiceCookies = false; return }
            guard case .success(let snapshot) = result, let values = snapshot["cookies"] as? [[String: Any]] else { self.syncingServiceCookies = false; return }
            self.webSession.apply(values.compactMap(WebSessionCookie.fromService)) {
                self.webSession.persist {
                    self.savedSessionVersion = (snapshot["version"] as? NSNumber)?.uint64Value
                    self.syncingServiceCookies = false
                    self.refreshLogin()
                }
            }
        }
    }
    func loginWebView() -> WKWebView {
        let configuration = WKWebViewConfiguration(); configuration.websiteDataStore = webSession.dataStore
        let web = WKWebView(frame: .zero, configuration: configuration)
        web.navigationDelegate = self; web.uiDelegate = self
        web.customUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15"
        return web
    }
    func openLogin() {
        guard webSession.ready else { webSession.restore { self.openLogin() }; return }
        if let window = loginWindow { window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        loginTimeout?.cancel(); loginWeb?.stopLoading(); loginWeb = nil; importingSession = false
        let web = loginWebView()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 900, height: 670), styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.title = "登录小米账号 · i.mi.com"; window.center(); window.isReleasedWhenClosed = false; window.contentView = web; window.delegate = self
        loginWeb = web; loginWindow = window
        web.load(URLRequest(url: URL(string: "https://i.mi.com/drive/h5")!))
        window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func refreshLogin() {
        guard webSession.ready, !webSession.needsAuthorization, !syncingServiceCookies, loginWeb == nil, refreshPolicy.begin() else { return }
        let web = loginWebView(); loginWeb = web
        let timeout = DispatchWorkItem { [weak self, weak web] in
            guard let self = self, let web = web, web === self.loginWeb else { return }
            self.finishLoginRefresh(web, result: .transientFailure)
        }
        loginTimeout = timeout; DispatchQueue.main.asyncAfter(deadline: .now() + 60, execute: timeout)
        let load = {
            guard web === self.loginWeb else { return }
            web.load(URLRequest(url: URL(string: "https://i.mi.com/drive/h5")!, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 40))
        }
        // A healthy service may have newer rotated cookies than the last browser
        // visit. Do not re-import stale browser tokens over an active session.
        if connected {
            request("session/cookies") { result in
                guard web === self.loginWeb else { return }
                if case .success(let state) = result, let values = state["cookies"] as? [[String: Any]] {
                    self.webSession.apply(values.compactMap(WebSessionCookie.fromService)) { self.webSession.persist(); load() }
                } else { load() }
            }
        } else { load() }
    }
    func finishLoginRefresh(_ web: WKWebView, result: LoginRefreshResult) {
        guard web === loginWeb else { return }
        let background = loginWindow == nil
        loginTimeout?.cancel(); loginTimeout = nil; importingSession = false
        refreshPolicy.finish(result: result)
        if result == .connected {
            webSession.persist(); loginWeb = nil
            let window = loginWindow; loginWindow = nil; window?.close()
            connected = true; nextMountAttempt = .distantPast; heartbeat(); model.refresh()
        } else if background {
            web.stopLoading(); loginWeb = nil
            request("session/refresh-failed", body: ["reason": result == .loginRequired ? "login_required" : "network"]) { _ in self.model.refresh() }
        }
    }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard webView === loginWeb else { return }
        let host = webView.url?.host ?? ""
        if ["account.xiaomi.com", "account.xiaomi.cn"].contains(host), webView.url?.path.contains("/login") == true {
            if loginWindow == nil { finishLoginRefresh(webView, result: .loginRequired) }; return
        }
        guard host == "i.mi.com", webView.url?.path.hasPrefix("/drive") == true, !importingSession else { return }
        importingSession = true
        webSession.dataStore.httpCookieStore.getAllCookies { cookies in
            guard webView === self.loginWeb else { return }
            let values = cookies.compactMap { WebSessionCookie($0).servicePayload }
            guard values.contains(where: { $0["Name"] as? String == "serviceToken" }) else {
                self.importingSession = false
                if self.loginWindow == nil { self.finishLoginRefresh(webView, result: .loginRequired) }; return
            }
            self.request("session", body: ["cookies": values, "background": self.loginWindow == nil]) { result in
                guard webView === self.loginWeb else { return }
                self.importingSession = false
                switch result {
                case .success: self.finishLoginRefresh(webView, result: .connected)
                case .failure(let error):
                    self.model.error = error.localizedDescription
                    if self.loginWindow == nil, (error as NSError).code != 401 { self.finishLoginRefresh(webView, result: .transientFailure) }
                    // On a rejected probe, keep the official page alive so its
                    // account SSO redirect can renew the session. A real login
                    // form or the deadline, not this early probe, ends renewal.
                }
            }
        }
    }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        if (error as NSError).code != NSURLErrorCancelled { finishLoginRefresh(webView, result: .transientFailure) }
    }
    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        if (error as NSError).code != NSURLErrorCancelled { finishLoginRefresh(webView, result: .transientFailure) }
    }
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { decisionHandler(.cancel); return }
        let host = url.host ?? ""
        if url.scheme == "https" && (["mi.com", "xiaomi.com", "xiaomi.cn", "mi-img.com", "xiaomi.net"].contains(where: { host == $0 || host.hasSuffix("."+$0) })) { decisionHandler(.allow) }
        else if url.absoluteString == "about:blank" { decisionHandler(.allow) } else { decisionHandler(.cancel) }
    }
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? { if webView === loginWeb, action.targetFrame == nil { webView.load(action.request) }; return nil }
    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) { let alert = NSAlert(); alert.messageText = message; alert.addButton(withTitle: "确认"); alert.addButton(withTitle: "取消"); completionHandler(alert.runModal() == .alertFirstButtonReturn) }
    func windowWillClose(_ notification: Notification) {
        if notification.object as? NSWindow === loginWindow {
            loginTimeout?.cancel(); loginTimeout = nil; loginWeb?.stopLoading()
            loginWindow = nil; loginWeb = nil; importingSession = false
            refreshPolicy.finish(result: .transientFailure)
        }
    }
    func setStartup(_ enabled: Bool) throws {
        let path = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/米盘").path
        if enabled {
            let plist: [String: Any] = ["Label": launchLabel, "ProgramArguments": [path, "--background"], "RunAtLoad": true, "KeepAlive": ["SuccessfulExit": false], "ProcessType": "Interactive", "LimitLoadToSessionType": "Aqua", "ThrottleInterval": 10]
            try FileManager.default.createDirectory(at: startupURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0).write(to: startupURL, options: .atomic)
        } else if startup { try FileManager.default.removeItem(at: startupURL) }
        // The current app already runs. launchd loads RunAtLoad at the next login.
        UserDefaults.standard.set(true, forKey: "hasChosenStartup")
        notifyState()
    }
    func state() -> [String: Any] { ["startup": startup, "mounted": isMounted()] }
    func notifyState() { model.refresh() }
    func clearLogin(completion: @escaping (String?) -> Void) {
        loginTimeout?.cancel(); loginTimeout = nil; loginWeb?.stopLoading(); loginWeb = nil
        let window = loginWindow; loginWindow = nil; window?.close()
        webSession.clear(completion: completion)
    }
    func showError(_ text: String) { let alert = NSAlert(); alert.messageText = "米盘"; alert.informativeText = text; alert.addButton(withTitle: "好"); NSApp.activate(ignoringOtherApps: true); alert.runModal() }
    @objc func quit() {
        request("status") { result in
            if case .success(let state) = result, TransferSafety.hasActive(state) { self.showError("文件正在传输，请等传输完成后再退出。"); return }
            self.stopping = true; self.unmount { error in if let error = error { self.stopping = false; self.showError(error); return }; self.service?.terminate(); NSApp.terminate(nil) }
        }
    }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply { if !stopping { quit(); return .terminateCancel }; return .terminateNow }
    func applicationWillTerminate(_ notification: Notification) { stopping = true; service?.terminate(); timer?.invalidate(); localName.close() }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool { showSettings(); return true }
}
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
