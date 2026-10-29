import SwiftUI

private enum TransferSection: String, CaseIterable, Identifiable {
    case active = "正在传输", completed = "已完成"
    var id: Self { self }
}
struct TransfersView: View {
    let snapshot: TransferSnapshot
    let recoveryCount: Int
    let showRecovery: () -> Void
    @State private var section: TransferSection = .active
    var body: some View {
        VStack(spacing: 14) {
            Text("传输").font(.system(size: 22, weight: .bold)).padding(.bottom, 6)
            Picker("传输列表", selection: $section) {
                ForEach(TransferSection.allCases) { item in
                    Text(item.rawValue + " \(item == .active ? snapshot.active.count : snapshot.completed.count)").tag(item)
                }
            }.pickerStyle(.segmented).labelsHidden().frame(width: 280).padding(.bottom, 4)
            if section == .active {
                if !snapshot.active.isEmpty {
                    HStack {
                        Text("\(snapshot.active.count) 个文件")
                        Spacer()
                        Text(FileTransfer.byteCount(snapshot.active.reduce(0) { $0 + $1.speed }) + "/s").monospacedDigit()
                    }.font(.system(size: 11)).foregroundStyle(.secondary)
                }
                files(snapshot.active, empty: "当前没有传输任务", icon: "arrow.up.arrow.down")
                if !snapshot.failed.isEmpty || recoveryCount > 0 {
                    HStack {
                        Text("未完成").font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary)
                        Spacer()
                        if recoveryCount > 0 { Button("查看副本", action: showRecovery).controlSize(.small) }
                    }.padding(.top, 6)
                    if !snapshot.failed.isEmpty {
                        files(snapshot.failed, empty: "", icon: "exclamationmark.circle")
                    } else {
                        SettingsCard { SettingsRow(icon: "exclamationmark.circle", title: "\(recoveryCount) 个文件需要重新传输") { EmptyView() } }
                    }
                }
            } else {
                files(snapshot.completed, empty: "暂无已完成的文件", icon: "checkmark.circle")
            }
            if let error = snapshot.error, !error.isEmpty {
                SettingsCard { SettingsRow(icon: "exclamationmark.circle", title: error) { EmptyView() } }
            }
        }
    }
    private func files(_ records: [FileTransfer], empty: String, icon: String) -> some View {
        SettingsCard {
            if records.isEmpty {
                SettingsRow(icon: icon, title: empty) { EmptyView() }
            } else {
                ForEach(records) { record in
                    if record.id != records.first?.id { CardDivider() }
                    TransferRow(record: record).equatable()
                }
            }
        }
    }
}
private struct TransferRow: View, Equatable {
    let record: FileTransfer
    private var color: Color { record.active ? .blue : record.completed ? .green : .orange }
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: record.direction == "upload" ? "arrow.up.circle" : "arrow.down.circle")
                .font(.system(size: 15)).foregroundStyle(color).frame(width: 20).padding(.top, 2)
            VStack(alignment: .leading, spacing: 7) {
                HStack(alignment: .top, spacing: 12) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(record.name).font(.system(size: 13, weight: .medium)).lineLimit(1).truncationMode(.middle).help(record.path)
                        Text(record.directionLabel + " · " + record.folder).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }.frame(maxWidth: .infinity, alignment: .leading)
                    VStack(alignment: .trailing, spacing: 3) {
                        Text(record.active ? (record.measurable ? record.speedLabel : record.phaseLabel) : record.completed ? record.sizeLabel : record.phaseLabel)
                            .font(.system(size: 12, weight: .medium)).monospacedDigit().foregroundStyle(record.active ? Color.primary : color)
                        if record.completed, let date = record.finishedAt {
                            Text(date.formatted(date: .abbreviated, time: .shortened)).font(.system(size: 10)).foregroundStyle(.secondary)
                        }
                    }.frame(width: 116, alignment: .trailing)
                }
                if record.active {
                    if record.measurable, let progress = record.progress {
                        ProgressView(value: progress).progressViewStyle(.linear)
                    } else { ProgressView().progressViewStyle(.linear) }
                    HStack {
                        Text(record.phaseLabel)
                        Spacer()
                        Text(record.progressLabel).monospacedDigit()
                    }.font(.system(size: 11)).foregroundStyle(.secondary)
                } else if !record.completed {
                    Text(record.failureLabel).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2).help(record.error ?? "")
                }
            }
        }.padding(.horizontal, 14).padding(.vertical, 12).accessibilityElement(children: .combine)
    }
}
