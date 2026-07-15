import { useQuery, useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { User, Lock, Save } from "lucide-react";
import { api } from "../api";

export default function AccountSettingsPage() {
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: api.getMe });
  const [name, setName] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const updateProfile = useMutation({
    mutationFn: () => api.updateProfile({ name }),
  });

  const changePassword = useMutation({
    mutationFn: () => api.changePassword({ current_password: currentPassword, new_password: newPassword }),
    onSuccess: () => { setCurrentPassword(""); setNewPassword(""); },
  });

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
            <input value={me?.email || ""} disabled
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-gray-500 text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Display Name</label>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder={me?.email}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => updateProfile.mutate()}
            disabled={updateProfile.isPending}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
            <Save className="w-4 h-4" /> {updateProfile.isPending ? "Saving..." : "Save"}
          </button>
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
          {changePassword.isSuccess && <p className="text-green-400 text-sm">Password updated. Please log in again.</p>}
          {changePassword.error && <p className="text-red-400 text-sm">{changePassword.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
