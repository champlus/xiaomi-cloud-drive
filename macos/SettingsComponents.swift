import SwiftUI

// Uses Lyli's settings layout metrics: a 20 pt icon column, 12 pt gap,
// 14 pt horizontal insets and 11 pt vertical insets.
struct SettingsCard<Content: View>: View {
    @ViewBuilder let content: () -> Content
    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 10, style: .continuous)
        VStack(spacing: 0, content: content)
            .background(shape.fill(Color.primary.opacity(0.05)))
            .overlay(shape.strokeBorder(Color.primary.opacity(0.07), lineWidth: 0.5))
    }
}
struct CardDivider: View {
    var body: some View { Divider().padding(.leading, 46) }
}
struct SettingsRow<Trailing: View>: View {
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
