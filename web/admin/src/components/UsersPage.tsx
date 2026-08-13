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

  if (isLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;
  if (error) return <p className="text-[var(--danger)]">Failed to load users: {(error as Error).message}</p>;

  const users = data || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)]">User Management</h2>

      {users.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)]">
          No users found.
        </div>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Email</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Role</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Status</th>
                <th className="text-right p-4 text-[var(--text-secondary)] font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.email} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)]">{u.email}</td>
                  <td className="p-4">
                    <span className="px-2 py-1 text-xs rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">
                      {u.role}
                    </span>
                  </td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      u.status === "active" ? "bg-[var(--success)]/10 text-[var(--success)]" :
                      u.status === "suspended" ? "bg-[var(--danger)]/10 text-[var(--danger)]" :
                      "bg-[var(--text-secondary)]/10 text-[var(--text-secondary)]"
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
                        className="text-[var(--danger)] hover:underline text-xs disabled:opacity-50"
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
        <p className="text-[var(--danger)] text-sm mt-2">Failed to delete user: {(deleteUser.error as Error).message}</p>
      )}
    </div>
  );
}
