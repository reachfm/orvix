import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";

interface PlatformUser {
  mailbox_id: number | null;
  user_id: number | null;
  email: string;
  role: string;
  is_admin: boolean;
  status: string;
}

export default function UsersPage() {
  const queryClient = useQueryClient();
  const { data, isLoading, error } = useQuery<PlatformUser[]>({
    queryKey: ["platform-users"],
    queryFn: api.listPlatformUsers,
  });

  const deleteUser = useMutation({
    mutationFn: (userId: number) => api.deleteUser(userId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["platform-users"] }),
  });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load users: {(error as Error).message}</p>;

  const users = data || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">User Management</h2>

      {users.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8]">
          No users found.
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Email</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Role</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.email} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0]">{u.email}</td>
                  <td className="p-4">
                    <span className="px-2 py-1 text-xs rounded-full bg-[#4F7CFF]/10 text-[#4F7CFF]">
                      {u.role}
                    </span>
                  </td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      u.status === "active" ? "bg-[#34D399]/10 text-[#34D399]" :
                      u.status === "suspended" ? "bg-[#F87171]/10 text-[#F87171]" :
                      "bg-[#8B92A8]/10 text-[#8B92A8]"
                    }`}>
                      {u.status}
                    </span>
                  </td>
                  <td className="p-4 text-right">
                    {u.user_id !== null && (
                      <button
                        onClick={() => {
                          if (window.confirm(`Delete user ${u.email}? This cannot be undone.`)) {
                            deleteUser.mutate(u.user_id as number);
                          }
                        }}
                        disabled={deleteUser.isPending}
                        className="text-[#F87171] hover:underline text-xs disabled:opacity-50"
                      >
                        Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {deleteUser.isError && (
        <p className="text-[#F87171] text-sm mt-2">Failed to delete user: {(deleteUser.error as Error).message}</p>
      )}
    </div>
  );
}
