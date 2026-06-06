import { useState } from "react";

export default function UsersPage() {
  const [users] = useState([
    { name: "John Doe", email: "john@example.com", role: "admin", quota: "10 GB", status: "active" },
    { name: "Jane Smith", email: "jane@example.com", role: "user", quota: "5 GB", status: "active" },
  ]);

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">User Management</h2>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E]">
              <th className="text-left p-4 text-[#8B92A8] font-medium">Name</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Email</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Role</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Quota</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
              <th className="text-right p-4 text-[#8B92A8] font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.email} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                <td className="p-4 text-[#E8EAF0]">{u.name}</td>
                <td className="p-4 text-[#8B92A8]">{u.email}</td>
                <td className="p-4">
                  <span className="px-2 py-1 text-xs rounded-full bg-[#4F7CFF]/10 text-[#4F7CFF]">
                    {u.role}
                  </span>
                </td>
                <td className="p-4 text-[#8B92A8]">{u.quota}</td>
                <td className="p-4">
                  <span className="px-2 py-1 text-xs rounded-full bg-[#34D399]/10 text-[#34D399]">
                    {u.status}
                  </span>
                </td>
                <td className="p-4 text-right space-x-2">
                  <button className="text-[#4F7CFF] hover:underline text-xs">Edit</button>
                  <button className="text-[#F87171] hover:underline text-xs">Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
