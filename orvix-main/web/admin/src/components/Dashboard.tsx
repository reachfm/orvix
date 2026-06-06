import { Inbox, Send, AlertTriangle, Activity } from "lucide-react";

export default function Dashboard() {
  const stats = [
    { label: "Emails Today", value: "12,847", icon: Inbox, color: "text-[#4F7CFF]" },
    { label: "Queue Depth", value: "234", icon: Send, color: "text-[#34D399]" },
    { label: "Spam Blocked", value: "1,892", icon: AlertTriangle, color: "text-[#FBBF24]" },
    { label: "Active Connections", value: "47", icon: Activity, color: "text-[#F87171]" },
  ];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Dashboard</h2>

      <div className="grid grid-cols-4 gap-4 mb-8">
        {stats.map((s) => {
          const Icon = s.icon;
          return (
            <div key={s.label} className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
              <div className="flex items-center gap-3 mb-3">
                <Icon size={20} className={s.color} />
                <span className="text-xs text-[#8B92A8]">{s.label}</span>
              </div>
              <p className="text-2xl font-bold text-[#E8EAF0]">{s.value}</p>
            </div>
          );
        })}
      </div>

      <div className="grid grid-cols-2 gap-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Delivery Rate</h3>
          <div className="flex items-end gap-2 h-32">
            {[85, 92, 88, 95, 91, 89, 94, 96, 93, 90, 97, 95].map((v, i) => (
              <div
                key={i}
                className="flex-1 bg-[#4F7CFF] rounded-t"
                style={{ height: `${v}%` }}
              />
            ))}
          </div>
        </div>

        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Server Health</h3>
          <div className="space-y-3">
            {[
              { label: "CPU", value: 23 },
              { label: "Memory", value: 45 },
              { label: "Disk", value: 62 },
              { label: "Network", value: 18 },
            ].map((m) => (
              <div key={m.label}>
                <div className="flex justify-between text-xs mb-1">
                  <span className="text-[#8B92A8]">{m.label}</span>
                  <span className="text-[#E8EAF0]">{m.value}%</span>
                </div>
                <div className="h-1.5 bg-[#222736] rounded-full overflow-hidden">
                  <div
                    className={`h-full rounded-full ${
                      m.value > 80 ? "bg-[#F87171]" : m.value > 60 ? "bg-[#FBBF24]" : "bg-[#34D399]"
                    }`}
                    style={{ width: `${m.value}%` }}
                  />
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
