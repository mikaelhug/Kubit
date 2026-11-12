import ServiceManagement

@MainActor
enum LoginItem {
    static var enabled: Bool { SMAppService.mainApp.status == .enabled }

    static func set(_ on: Bool) {
        let service = SMAppService.mainApp
        do {
            if on {
                if service.status != .enabled { try service.register() }
            } else if service.status == .enabled {
                try service.unregister()
            }
        } catch {
            if service.status != .requiresApproval {
                Updater.alert("Kubit could not change its login item.", detail: error.localizedDescription)
            }
        }
        if on && service.status == .requiresApproval { SMAppService.openSystemSettingsLoginItems() }
    }
}
