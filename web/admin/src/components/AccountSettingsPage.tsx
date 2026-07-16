import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, useEffect } from "react";
import { User, Lock, Save, Check, Loader2 } from "lucide-react";
import { api } from "../api";

export default function AccountSettingsPage() {
  const queryClient = useQueryClient();
  const { data: profile, isLoading: profileLoading } = useQuery({ queryKey: ["profile"], queryFn: api.getProfile });

  const [displayName, setDisplayName] = useState("");
  const [locale, setLocale] = useState("");
  const [timezone, setTimezone] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  useEffect(() => {
    if (profile) {
      setDisplayName(profile.display_name || "");
      setLocale(profile.locale || "");
      setTimezone(profile.timezone || "");
    }
  }, [profile]);

  const updateProfile = useMutation({
    mutationFn: () => api.updateProfile({ display_name: displayName, locale, timezone }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["profile"] }),
  });

  const changePassword = useMutation({
    mutationFn: () => api.changePassword({ current_password: currentPassword, new_password: newPassword }),
    onSuccess: () => { setCurrentPassword(""); setNewPassword(""); },
  });

  if (profileLoading) {
    return (
      <div className="flex items-center gap-2 text-[#8B92A8] text-sm py-8">
        <Loader2 className="w-4 h-4 animate-spin" />
        Loading profile...
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-white">Account Settings</h2>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <User className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Profile</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Email</label>
            <input value={profile?.email || ""} disabled
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-gray-500 text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Role</label>
            <input value={profile?.role || ""} disabled
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-gray-500 text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Display Name</label>
            <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="Display name"
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Locale</label>
            <input value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="en-US"
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Timezone</label>
            <input value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="UTC"
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => updateProfile.mutate()}
            disabled={updateProfile.isPending}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
            <Save className="w-4 h-4" /> {updateProfile.isPending ? "Saving..." : "Save"}
          </button>
          {updateProfile.isSuccess && (
            <div className="flex items-center gap-2 text-[#34D399] text-sm">
              <Check className="w-4 h-4" /> Profile updated successfully.
            </div>
          )}
          {updateProfile.error && (
            <p className="text-[#F87171] text-sm">{(updateProfile.error as any)?.message || "Failed to update profile"}</p>
          )}
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Lock className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Change Password</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Current Password</label>
            <input type="password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">New Password</label>
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => changePassword.mutate()}
            disabled={changePassword.isPending || !currentPassword || !newPassword}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
            <Lock className="w-4 h-4" /> {changePassword.isPending ? "Changing..." : "Update Password"}
          </button>
          {changePassword.isSuccess && <p className="text-[#34D399] text-sm">Password updated.</p>}
          {changePassword.error && <p className="text-[#F87171] text-sm">{(changePassword.error as any)?.message || "Failed to update password"}</p>}
        </div>
      </div>
    </div>
  );
}
