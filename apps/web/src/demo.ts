// Explicit preview fixtures. Never mixed into live server responses.
export type Device = {
  name: string;
  address: string;
  cpu: number;
  memory: number;
  disk: number;
  network: number;
  version: string;
  status: "Healthy" | "Offline" | "Needs access";
  role: string;
};
export const demoDevices: Device[] = [
  {
    name: "atlas",
    address: "10.20.0.10",
    cpu: 18.4,
    memory: 42.8,
    disk: 34.2,
    network: 2.41,
    version: "0.1.0",
    status: "Healthy",
    role: "Proxmox host",
  },
  {
    name: "observatory",
    address: "10.20.0.11",
    cpu: 6.2,
    memory: 28.6,
    disk: 18.3,
    network: 0.12,
    version: "0.1.0",
    status: "Healthy",
    role: "Scout server",
  },
  {
    name: "archive",
    address: "10.20.0.12",
    cpu: 8.1,
    memory: 54.2,
    disk: 82.6,
    network: 12.84,
    version: "0.1.0",
    status: "Healthy",
    role: "Storage",
  },
  {
    name: "forge",
    address: "10.20.0.20",
    cpu: 38.7,
    memory: 63.1,
    disk: 46.8,
    network: 1.64,
    version: "0.1.0",
    status: "Healthy",
    role: "Build runner",
  },
  {
    name: "relay",
    address: "10.20.0.21",
    cpu: 2.3,
    memory: 16.5,
    disk: 12.4,
    network: 0.06,
    version: "0.1.0",
    status: "Healthy",
    role: "Application server",
  },
  {
    name: "harbor",
    address: "10.20.0.22",
    cpu: 0,
    memory: 0,
    disk: 0,
    network: 0,
    version: "0.1.0",
    status: "Offline",
    role: "Application server",
  },
  {
    name: "10.20.0.30",
    address: "10.20.0.30",
    cpu: 0,
    memory: 0,
    disk: 0,
    network: 0,
    version: "—",
    status: "Needs access",
    role: "Discovered device",
  },
];
