import { create } from 'zustand'
import { persist } from 'zustand/middleware'
interface WorkspaceState {
  currentId: string | null
  setCurrent: (id: string | null) => void
}
export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set) => ({
      currentId: null,
      setCurrent: (id) => set({ currentId: id }),
    }),
    { name: 'orkome.workspace' },
  ),
)
