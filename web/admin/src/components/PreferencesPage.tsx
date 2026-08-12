import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Bell, Globe, Moon, Sun, Save, Loader2 } from "lucide-react";
import { api } from "../api";
import { useTheme } from "../shared/theme/useTheme";

export default function PreferencesPage() {
  const queryClient = useQueryClient();
  const { data: profile, isLoading, isError } = useQuery({
    queryKey: ["profile"],
    queryFn: api.getProfile,
  });

  const [emailNotifications, setEmailNotifications] = useState(true);
  const [inAppNotifications, setInAppNotifications] = useState(true);
  const [loginAlerts, setLoginAlerts] = useState(true);
  const [billingAlerts, setBillingAlerts] = useState(true);
  const [language, setLanguage] = useState("en");
  const [timezone, setTimezone] = useState("UTC");
  // Theme is a client-only preference (persisted to localStorage under
  // orvix-admin-theme, applied pre-paint) — it is never sent to or read
  // from the backend profile, unlike the notification/locale settings
  // below which are real server-persisted fields.
  const { theme, toggleTheme } = useTheme();
  const darkMode = theme === "dark";

  useEffect(() => {
    if (profile) {
      setLanguage(profile.locale || profile.language || "en");
      setTimezone(profile.timezone || "UTC");
      setEmailNotifications(profile.email_notifications ?? true);
      setInAppNotifications(profile.in_app_notifications ?? true);
      setLoginAlerts(profile.login_alerts ?? true);
      setBillingAlerts(profile.billing_alerts ?? true);
    }
  }, [profile]);

  const savePreferences = useMutation({
    mutationFn: async () =>
      api.updateProfile({
        email_notifications: emailNotifications,
        in_app_notifications: inAppNotifications,
        login_alerts: loginAlerts,
        billing_alerts: billingAlerts,
        locale: language,
        timezone,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["profile"] });
    },
  });

  const timezones = [
    "UTC", "US/Eastern", "US/Central", "US/Mountain", "US/Pacific",
    "Europe/London", "Europe/Berlin", "Asia/Dubai", "Asia/Tokyo",
  ];
  const languages = [
    { value: "en", label: "English" },
    { value: "ar", label: "Arabic" },
    { value: "fr", label: "French" },
    { value: "de", label: "German" },
    { value: "es", label: "Spanish" },
  ];

  if (isLoading) {
    return (
      <div className="space-y-6 max-w-2xl">
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Preferences</h2>
        <div className="flex items-center gap-3 text-[var(--text-secondary)] text-sm">
          <Loader2 className="w-4 h-4 animate-spin" /> Loading preferences...
        </div>
      </div>
    );
  }

  if (isError) {
    return (
      <div className="space-y-6 max-w-2xl">
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Preferences</h2>
        <p className="text-[var(--danger)] text-sm">Failed to load preferences. Please try again later.</p>
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Preferences</h2>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Bell className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Email Notifications</h3>
        </div>
        <p className="text-xs text-[var(--text-secondary)] mb-3">Control which emails you receive from the platform.</p>
        <div className="space-y-3">
          <ToggleRow label="Transactional Emails" description="Delivery receipts, bounce alerts, and system notices" checked={emailNotifications} onChange={setEmailNotifications} />
          <ToggleRow label="In-App Notifications" description="Show alerts and banners within the dashboard" checked={inAppNotifications} onChange={setInAppNotifications} />
          <ToggleRow label="Login Alerts" description="Notify on new sign-in from unknown devices" checked={loginAlerts} onChange={setLoginAlerts} />
          <ToggleRow label="Billing Alerts" description="Notify about upcoming charges and invoices" checked={billingAlerts} onChange={setBillingAlerts} />
        </div>
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Globe className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Display</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Language</label>
            <select value={language} onChange={(e) => setLanguage(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm">
              {languages.map((l) => (
                <option key={l.value} value={l.value}>{l.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Timezone</label>
            <select value={timezone} onChange={(e) => setTimezone(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm">
              {timezones.map((tz) => (
                <option key={tz} value={tz}>{tz}</option>
              ))}
            </select>
          </div>
        </div>
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          {darkMode ? <Moon className="w-5 h-5 text-[var(--accent)]" /> : <Sun className="w-5 h-5 text-[var(--accent)]" />}
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Appearance</h3>
        </div>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--text-primary)]">Dark Mode</p>
            <p className="text-xs text-[var(--text-secondary)]">Toggle dark theme for the console</p>
          </div>
          <button
            role="switch"
            aria-checked={darkMode}
            aria-label="Toggle dark theme"
            onClick={toggleTheme}
            className={`relative w-11 h-6 rounded-full transition-colors ${darkMode ? "bg-[var(--accent)]" : "bg-[var(--border)]"}`}
          >
            <span className={`absolute top-0.5 w-5 h-5 rounded-full bg-white transition-transform ${darkMode ? "translate-x-5" : "translate-x-0.5"}`} />
          </button>
        </div>
      </div>

      <button onClick={() => savePreferences.mutate()}
        disabled={savePreferences.isPending}
        className="flex items-center gap-2 bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
        <Save className="w-4 h-4" /> {savePreferences.isPending ? "Saving..." : "Save Preferences"}
      </button>
      {savePreferences.isSuccess && <p className="text-[var(--success)] text-sm">Preferences saved.</p>}
      {savePreferences.error && <p className="text-[var(--danger)] text-sm">{(savePreferences.error as any)?.message || "Failed to save preferences"}</p>}
    </div>
  );
}

function ToggleRow({ label, description, checked, onChange }: {
  label: string; description: string; checked: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between py-2">
      <div>
        <p className="text-sm text-[var(--text-primary)]">{label}</p>
        <p className="text-xs text-[var(--text-secondary)]">{description}</p>
      </div>
      <button
        onClick={() => onChange(!checked)}
        className={`relative w-10 h-5 rounded-full transition-colors ${checked ? "bg-[var(--accent)]" : "bg-[var(--border)]"}`}
      >
        <span className={`absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform ${checked ? "translate-x-5" : "translate-x-0.5"}`} />
      </button>
    </div>
  );
}
