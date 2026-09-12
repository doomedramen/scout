import {
  ChevronRight,
  Cpu,
  Download,
  HardDrive,
  KeyRound,
  ScanSearch,
  ServerCog,
  Settings2,
  Terminal,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";

type SettingsDestination = "Scopes" | "Enrollment" | "Access" | "Updates" | "Services" | "Hardware" | "Storage";

type SettingsItem = {
  name: SettingsDestination;
  description: string;
  icon: typeof ScanSearch;
};

const groups: Array<{ title: string; items: SettingsItem[] }> = [
  {
    title: "Administration",
    items: [
      { name: "Scopes", description: "Sites and ranges", icon: ScanSearch },
      { name: "Enrollment", description: "Found hosts", icon: ServerCog },
      { name: "Access", description: "Credentials and trust", icon: KeyRound },
      { name: "Updates", description: "Agent versions", icon: Download },
    ],
  },
  {
    title: "Diagnostics",
    items: [
      { name: "Services", description: "Collectors", icon: Terminal },
      { name: "Hardware", description: "Sensors and GPUs", icon: Cpu },
      { name: "Storage", description: "Disks and pools", icon: HardDrive },
    ],
  },
];

export function SettingsView({ onNavigate }: { onNavigate: (page: SettingsDestination) => void }) {
  return (
    <section className="settings-view" aria-labelledby="settings-title">
      <div className="settings-intro">
        <Badge variant="outline">
          <Settings2 size={13} />
          Workspace
        </Badge>
        <h2 id="settings-title">Settings</h2>
      </div>
      <div className="settings-grid">
        {groups.map((group) => (
          <section
            className="settings-group"
            key={group.title}
            aria-labelledby={`settings-${group.title.toLowerCase()}`}
          >
            <div className="settings-group-heading">
              <h3 id={`settings-${group.title.toLowerCase()}`}>{group.title}</h3>
            </div>
            <div className="settings-items">
              {group.items.map(({ name, description, icon: Icon }) => (
                <button type="button" className="settings-item" key={name} onClick={() => onNavigate(name)}>
                  <span className="settings-item-icon" aria-hidden="true">
                    <Icon size={17} />
                  </span>
                  <span className="settings-item-copy">
                    <strong>{name}</strong>
                    <small>{description}</small>
                  </span>
                  <ChevronRight className="settings-item-arrow" size={16} aria-hidden="true" />
                </button>
              ))}
            </div>
          </section>
        ))}
      </div>
    </section>
  );
}
