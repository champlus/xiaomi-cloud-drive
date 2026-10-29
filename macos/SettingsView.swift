import SwiftUI
import AppKit

struct Activity: Identifiable {
    let id: Int
    let text: String
    let kind: String
    let time: Date
}
final class SettingsModel: ObservableObject {
    weak var app: AppDelegate?
    @Published var connected = false
    @Published var checking = false
    @Published var renewing = false
    @Published var authRequired = false
    @Published var hasSession = false
    @Published var keychainRequired = false
    @Published var mounted = false
    @Published var startup = false
    @Published var autoMount = true
    @Published var readOnly = false
    @Published var busy = false
    @Published var error = ""
    @Published var pending = 0
    @Published var activities: [Activity] = []
    init(app: AppDelegate) { self.app = app }
    func refresh() {
        guard let app = app else { return }
        mounted = app.isMounted(); startup = app.startup; keychainRequired = app.webSession.needsAuthorization
        app.request("status") { [weak self] result in
            guard let self = self else { return }
            switch result {
            case .failure(let err): self.error = err.localizedDescription
            case .success(let state):
                self.connected = state["connected"] as? Bool ?? false
                self.checking = state["checking"] as? Bool ?? false
                self.renewing = state["renewing"] as? Bool ?? false
                self.authRequired = state["authRequired"] as? Bool ?? false
                self.hasSession = state["hasSession"] as? Bool ?? false
                self.error = state["error"] as? String ?? ""
                self.pending = (state["pending"] as? [[String: Any]])?.count ?? 0
                let config = state["config"] as? [String: Any] ?? [:]
                self.autoMount = config["autoMount"] as? Bool ?? true
                self.readOnly = config["readOnly"] as? Bool ?? false
                let formatter = ISO8601DateFormatter(); formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
                self.activities = (state["events"] as? [[String: Any]] ?? []).enumerated().map { index, value in
                    Activity(id: index, text: value["text"] as? String ?? "", kind: value["kind"] as? String ?? "", time: formatter.date(from: value["time"] as? String ?? "") ?? Date())
                }.suffix(4).reversed()
            }
        }
    }
    func perform(_ work: (@escaping (String?) -> Void) -> Void) {
        guard !busy else { return }; busy = true; error = ""
        work { [weak self] error in self?.busy = false; self?.refresh(); if let error = error { self?.error = error } }
    }
    func mount() { perform { done in self.app?.mount(open: true, completion: done) } }
    func unmount() { perform { done in self.app?.unmount(completion: done) } }
    func login() { app?.openLogin() }
    func authorizeKeychain() { perform { done in self.app?.authorizeKeychain(completion: done) } }
    func changeStartup(_ enabled: Bool) { do { try app?.setStartup(enabled); startup = enabled } catch { self.error = error.localizedDescription } }
    func changeConfig(autoMount: Bool? = nil, readOnly: Bool? = nil) {
        perform { done in self.app?.request("config", body: ["autoMount": autoMount ?? self.autoMount, "readOnly": readOnly ?? self.readOnly]) { result in
            switch result { case .failure(let err): done(err.localizedDescription); case .success: self.app?.nextMountAttempt = .distantPast; self.app?.heartbeat(); done(nil) }
        } }
    }
    func check() { app?.refreshPolicy.request(); perform { done in self.app?.request("check", body: [:]) { result in if case .failure(let err) = result { done(err.localizedDescription) } else { done(nil) } } } }
    func logout() { perform { done in self.app?.unmount { error in
        if let error = error { done(error); return }
        self.app?.request("logout", body: [:]) { result in
            if case .failure(let error) = result { done(error.localizedDescription); return }
            self.app?.clearLogin(completion: done)
        }
    } } }
}
enum SettingsTab: String, CaseIterable, Identifiable {
    case cloud, general, transfers
    var id: Self { self }
    var title: String { switch self { case .cloud: return "云盘"; case .general: return "通用"; case .transfers: return "传输" } }
    var icon: String { switch self { case .cloud: return "externaldrive.badge.icloud"; case .general: return "gearshape"; case .transfers: return "arrow.up.arrow.down" } }
    var tint: Color { switch self { case .cloud: return .cyan; case .general: return .gray; case .transfers: return .blue } }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    @State private var selection: SettingsTab? = .cloud
    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                Color.clear.frame(height: 20).accessibilityHidden(true)
                List(selection: $selection) {
                    ForEach(SettingsTab.allCases) { tab in
                        Label {
                            HStack {
                                Text(tab.title)
                                if tab == .transfers && model.pending > 0 {
                                    Spacer()
                                    Text("\(model.pending)").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        } icon: {
                            Image(systemName: tab.icon).font(.system(size: 12, weight: .medium))
                                .foregroundStyle(.white).frame(width: 20, height: 20)
                                .background(tab.tint.gradient, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
                        }
                        .padding(.vertical, 2).tag(tab)
                    }
                }.listStyle(.sidebar)
            }
            .navigationSplitViewColumnWidth(min: 165, ideal: 180, max: 210)
            .toolbar(removing: .sidebarToggle)
        } detail: {
            ScrollView {
                VStack(spacing: 14) {
                    switch selection ?? .cloud {
                    case .cloud: cloudPage
                    case .general: generalPage
                    case .transfers: transfersPage
                    }
                }
                .frame(maxWidth: 560)
                .padding(.horizontal, 24).padding(.top, 26).padding(.bottom, 28)
                .frame(maxWidth: .infinity)
            }
            .background(Color(nsColor: .windowBackgroundColor))
            .navigationTitle("米盘")
        }
        .frame(minWidth: 700, idealWidth: 760, minHeight: 460, idealHeight: 520)
        .onAppear { model.refresh() }
        .onReceive(Timer.publish(every: 3, on: .main, in: .common).autoconnect()) { _ in if !model.busy { model.refresh() } }
    }
    private func title(_ text: String) -> some View {
        Text(text).font(.system(size: 22, weight: .bold)).frame(maxWidth: .infinity).padding(.bottom, 6)
    }
    private var cloudPage: some View {
        Group {
            VStack(spacing: 6) {
                Image(nsImage: NSApp.applicationIconImage).resizable().frame(width: 76, height: 76).padding(.bottom, 2)
                title("小米云盘")
            }
            SettingsCard {
                SettingsRow(icon: "circle.dotted", title: "连接状态") {
                    HStack(spacing: 6) {
                        if model.checking || model.busy || model.renewing { ProgressView().controlSize(.mini) }
                        Text(model.keychainRequired ? "需要授权" : model.renewing ? "恢复连接中" : model.checking ? "连接中" : model.connected ? "已连接" : model.authRequired ? "需要验证" : model.hasSession ? "连接中断" : "未登录")
                            .font(.system(size: 12)).foregroundStyle(model.connected ? Color.green : Color.secondary)
                        Button(action: model.check) { Image(systemName: "arrow.clockwise") }.buttonStyle(.borderless).help("检查连接")
                    }
                }
                CardDivider()
                SettingsRow(icon: "macwindow", title: "Finder") {
                    if model.mounted { Button("弹出", action: model.unmount).disabled(model.busy) }
                    Button(model.mounted ? "打开" : "挂载", action: model.mount).disabled(!model.connected || model.busy)
                }
                CardDivider()
                SettingsRow(icon: "person.crop.circle", title: "小米账号") {
                    if model.keychainRequired { Button("授权钥匙串", action: model.authorizeKeychain).buttonStyle(.borderedProminent) }
                    else if model.connected {
                        Button("重新登录", action: model.login)
                        Button("退出", action: model.logout)
                    } else { Button("登录", action: model.login).buttonStyle(.borderedProminent) }
                }
            }
            SettingsCard {
                SettingsRow(icon: "safari", title: "小米云盘网页版") {
                    Button("打开") { NSWorkspace.shared.open(URL(string: "https://i.mi.com/drive/h5")!) }
                }
            }
            if !model.error.isEmpty && !model.renewing && !model.keychainRequired { errorRow }
        }
    }
    private var generalPage: some View {
        Group {
            title("通用")
            SettingsCard {
                SettingsRow(icon: "power", title: "登录时启动") {
                    Toggle("登录时启动", isOn: Binding(get: { model.startup }, set: model.changeStartup))
                }
                CardDivider()
                SettingsRow(icon: "externaldrive", title: "自动挂载") {
                    Toggle("自动挂载", isOn: Binding(get: { model.autoMount }, set: { model.changeConfig(autoMount: $0) }))
                }
                CardDivider()
                SettingsRow(icon: "lock", title: "只读访问") {
                    Toggle("只读访问", isOn: Binding(get: { model.readOnly }, set: { model.changeConfig(readOnly: $0) }))
                }
            }.disabled(model.busy)
        }
    }
    private var transfersPage: some View {
        Group {
            title("传输")
            if model.pending > 0 {
                SettingsCard {
                    SettingsRow(icon: "arrow.up.circle", title: "\(model.pending) 项未完成") {
                        Button("查看文件") { if let app = model.app { NSWorkspace.shared.open(app.support.appendingPathComponent("待恢复文件")) } }
                    }
                }
            }
            SettingsCard {
                if model.activities.isEmpty {
                    SettingsRow(icon: "clock", title: "暂无传输记录") { EmptyView() }
                } else {
                    ForEach(model.activities) { activity in
                        if activity.id != model.activities.first?.id { CardDivider() }
                        SettingsRow(icon: activity.kind == "error" ? "exclamationmark.circle" : activity.kind == "transfer" ? "arrow.up.circle" : "checkmark.circle", title: activity.text) {
                            Text(activity.time, style: .time).font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                    }
                }
            }
            if !model.error.isEmpty && !model.renewing && !model.keychainRequired { errorRow }
        }
    }
    private var errorRow: some View {
        SettingsCard {
            SettingsRow(icon: "exclamationmark.circle", title: model.error) { Button("重试", action: model.check) }
        }
    }
}

// Uses Lyli's settings layout metrics: a 20 pt icon column, 12 pt gap,
// 14 pt horizontal insets and 11 pt vertical insets.
private struct SettingsCard<Content: View>: View {
    @ViewBuilder let content: () -> Content
    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 10, style: .continuous)
        VStack(spacing: 0, content: content)
            .background(shape.fill(Color.primary.opacity(0.05)))
            .overlay(shape.strokeBorder(Color.primary.opacity(0.07), lineWidth: 0.5))
    }
}
private struct CardDivider: View {
    var body: some View { Divider().padding(.leading, 46) }
}
private struct SettingsRow<Trailing: View>: View {
    let icon: String
    let title: String
    @ViewBuilder let trailing: () -> Trailing
    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            Image(systemName: icon).font(.system(size: 13)).foregroundStyle(.secondary).frame(width: 20)
            Text(title).font(.system(size: 13))
            Spacer(minLength: 12)
            HStack(spacing: 8, content: trailing).labelsHidden().toggleStyle(.switch).fixedSize(horizontal: true, vertical: false)
        }
        .padding(.horizontal, 14).padding(.vertical, 11)
    }
}
