import SwiftUI

/// Settings › hub › Addresses (#4670, docs/operations/network.md "Client-added candidates"): other addresses of one paired
/// hub, tried after the paired one in the order shown (`HubRoute`). Each may carry its own Cloudflare Access service
/// token, sent to that address only.
struct HubAddressesPage: View {
    @Environment(AppModel.self) private var model
    let hubID: String
    @State private var url = ""
    @State private var clientID = ""
    @State private var clientSecret = ""
    @State private var showAccess = false

    var body: some View {
        if let hub = model.registry.hubs.first(where: { $0.id == hubID }) {
            form(hub).detailTitle(String(localized: "Addresses"))
        } else {
            EmptyStateView(symbol: Theme.Icon.hubAddresses, title: String(localized: "This Hub Is Disconnected"), message: String(localized: "Go back to Settings › Hub to pair again."))
        }
    }

    private func form(_ hub: PairedHub) -> some View {
        let added = hub.addresses ?? []
        let inUse = model.isLive(hub.group) ? model.socket(hub.group)?.currentConfig?.url : nil
        return Form {
            Section {
                row(hub.config.url, access: hub.config.access != nil, inUse: inUse == hub.config.url, paired: true)
                ForEach(Array(added.enumerated()), id: \.element.url) { i, a in
                    row(a.url, access: a.access != nil, inUse: inUse == a.url, paired: false)
                        .contextMenu { menu(i, of: added) }
                }
                .onDelete { offsets in save(added) { $0.remove(atOffsets: offsets) } }
                .onMove { from, to in save(added) { $0.move(fromOffsets: from, toOffset: to) } }
            } header: {
                SettingsHeading(String(localized: "Order to Try"))
            } footer: {
                Text("The app asks every address at once and uses the first in this order that answers; a later one when the earlier ones don't answer quickly. The paired address always comes first.")
            }
            Section {
                #if os(macOS)
                TextField(String(localized: "Address"), text: $url, prompt: Text(verbatim: "https://hub.example.com"))
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("addresses.url")
                #else
                TextField(String(localized: "Address"), text: $url, prompt: Text(verbatim: "https://hub.example.com"))
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("addresses.url")
                #endif
                // Closed until needed, as in the pairing sheet: most addresses have no token.
                DisclosureGroup(isExpanded: $showAccess) {
                    CloudflareAccessFields(clientID: $clientID, clientSecret: $clientSecret, idPrefix: "addresses.access")
                } label: {
                    Label("Cloudflare Access", systemImage: Theme.Icon.cloudflareAccess).lineLimit(1)
                }
                .accessibilityIdentifier("addresses.access")
            } header: {
                SettingsHeading(String(localized: "Add Address"))
            } footer: {
                Text("Another way to reach this same hub, such as a public domain when the paired address is a VPN one. Fill in both Cloudflare Access fields only if this address is behind Access; the token is sent to this address only.")
            }
            // In a section of its own, as Cloudflare Access's Save: the fields' card keeps its rounded end.
            Section {
                Button { add(hub, added) } label: {
                    Text("Add Address").lineLimit(1).minimumScaleFactor(0.8).frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                .disabled(draft(hub) == nil)
                .accessibilityIdentifier("addresses.add")
                #if os(iOS) || os(visionOS)
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
                #endif
            }
        }
        .themedPage()
        #if os(macOS)
        .formStyle(.grouped)
        #else
        .pageToolbar { if added.count > 1 { ToolbarItem(placement: .topBarTrailing) { EditButton() } } }
        #endif
    }

    private func row(_ url: URL, access: Bool, inUse: Bool, paired: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(url.absoluteString).lineLimit(1).truncationMode(.middle)
            HStack(spacing: Theme.Space.sm) {
                if paired { Chip(text: String(localized: "Paired"), look: .neutral) }
                if access { Chip(text: String(localized: "Cloudflare Access"), look: .neutral) }
                if inUse { Chip(text: String(localized: "In Use [Hubs View 564c83]"), look: .outline) }
            }
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private func menu(_ i: Int, of added: [HubAddress]) -> some View {
        if i > 0 {
            Button { save(added) { $0.swapAt(i, i - 1) } } label: { Label("Move Up", systemImage: Theme.Icon.up) }
        }
        if i < added.count - 1 {
            Button { save(added) { $0.swapAt(i, i + 1) } } label: { Label("Move Down", systemImage: Theme.Icon.down) }
        }
        Button(role: .destructive) { save(added) { $0.remove(at: i) } } label: { Label("Remove Address", systemImage: Theme.Icon.delete) }
    }

    /// The address typed, as one to add: http or https with a host, not one the hub has already.
    private func draft(_ hub: PairedHub) -> HubAddress? {
        let text = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let u = URL(string: text.hasSuffix("/") ? String(text.dropLast()) : text), ["http", "https"].contains(u.scheme?.lowercased() ?? ""),
              u.host()?.isEmpty == false, !hub.configs.contains(where: { $0.url == u }) else { return nil }
        let id = clientID.trimmingCharacters(in: .whitespacesAndNewlines), secret = clientSecret.trimmingCharacters(in: .whitespacesAndNewlines)
        guard id.isEmpty == secret.isEmpty else { return nil } // half a token: neither
        return HubAddress(url: u, access: HubAccess(clientID: id, clientSecret: secret))
    }

    private func add(_ hub: PairedHub, _ added: [HubAddress]) {
        guard let a = draft(hub) else { return }
        save(added) { $0.append(a) }
        url = ""; clientID = ""; clientSecret = ""; showAccess = false
        Haptics.success()
    }

    private func save(_ added: [HubAddress], _ change: (inout [HubAddress]) -> Void) {
        var list = added
        change(&list)
        model.setHubAddresses(list, hub: hubID)
    }
}
