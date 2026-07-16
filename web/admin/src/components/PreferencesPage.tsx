import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Bell, Globe, Moon, Sun, Save } from "lucide-react";
import { api } from "../api";

export default function PreferencesPage() {
  const queryClient = useQueryClient();
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: api.getMe });

  const [emailNotifications, setEmailNotifications] = useState(true);
  const [inAppNotifications, setInAppNotifications] = useState(true);
  const [loginAlerts, setLoginAlerts] = useState(true);
  const [billingAlerts, setBillingAlerts] = useState(true);
  const [language, setLanguage] = useState("en");
  const [timezone, setTimezone] = useState("UTC");
  const [darkMode, setDarkMode] = useState(true);

  const savePreferences = useMutation({
    mutationFn: async () => api.updateProfile({
      preferences: {
        email_notifications: emailNotifications,
        in_app_notifications: inAppNotifications,
        login_alerts: loginAlerts,
        billing_alerts: billingAlerts,
        language,
        timezone,
        dark_mode: darkMode,
      },
    }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["me"] }),
  });

  const timezones = ["UTC", "US/Eastern", "US/Central", "US/Mountain", "US/Pacific", "Europe/London", "Europe/Berlin", "Asia/Dubai", "Asia/Tokyo"];
  const languages = [
    { value: "en", label: "English" },
    { value: "ar", label: "Arabic" },
    { value: "fr", label: "French" },
    { value: "de", label: "German" },
    { value: "es", label: "Spanish" },
  ];

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-white">Preferences</h2>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Bell className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Notifications</h3>
        </div>
        <div className="space-y-3">
          <ToggleRow label="Email Notifications" description="Receive alerts via email" checked={emailNotifications} onChange={setEmailNotifications} />
          <ToggleRow label="In-App Notifications" description="Show alerts within the dashboard" checked={inAppNotifications} onChange={setInAppNotifications} />
          <ToggleRow label="Login Alerts" description="Notify on new sign-in from unknown devices" checked={loginAlerts} onChange={setLoginAlerts} />
          <ToggleRow label="Billing Alerts" description="Notify about upcoming charges and invoices" checked={billingAlerts} onChange={setBillingAlerts} />
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Globe className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Display</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Language</label>
            <select value={language} onChange={(e) => setLanguage(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm">
              {languages.map((l) => (
                <option key={l.value} value={l.value}>{l.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Timezone</label>
            <select value={timezone} onChange={(e) => setTimezone(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm">
              {timezones.map((tz) => (
                <option key={tz} value={tz}>{tz}</option>
              ))}
            </select>
          </div>
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Moon className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Appearance</h3>
        </div>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-white">Dark Mode</p>
            <p className="text-xs text-gray-400">Toggle dark theme for the console</p>
          </div>
          <button
            onClick={() => setDarkMode(!darkMode)}
            className={`relative w-11 h-6 rounded-full transition-colors ${darkMode ? "bg-[#4F7CFF]" : "bg-[#2A2F3E]"}`}
          >
            <span className={`absolute top-0.5 w-5 h-5 rounded-full bg-white transition-transform ${darkMode ? "translate-x-5" : "translate-x-0.5"}`} />
          </button>
        </div>
      </div>

      <button onClick={() => savePreferences.mutate()}
        disabled={savePreferences.isPending}
        className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
        <Save className="w-4 h-4" /> {savePreferences.isPending ? "Saving..." : "Save Preferences"}
      </button>
      {savePreferences.isSuccess && <p className="text-[#34D399] text-sm">Preferences saved.</p>}
      {savePreferences.error && <p className="text-[#F87171] text-sm">{(savePreferences.error as any)?.message || "Failed to save preferences"}</p>}
    </div>
  );
}

function ToggleRow({ label, description, checked, onChange }: {
  label: string; description: string; checked: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between py-2">
      <div>
        <p className="text-sm text-white">{label}</p>
        <p className="text-xs text-gray-400">{description}</p>
      </div>
      <button
        onClick={() => onChange(!checked)}
        className={`relative w-10 h-5 rounded-full transition-colors ${checked ? "bg-[#4F7CFF]" : "bg-[#2A2F3E]"}`}
      >
        <span className={`absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform ${checked ? "translate-x-5" : "translate-x-0.5"}`} />
      </button>
    </div>
  );
}
