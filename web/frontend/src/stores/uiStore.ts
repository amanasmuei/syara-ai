import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface UIState {
  isSidebarOpen: boolean;
  isCitationPanelOpen: boolean;
  isCitationModalOpen: boolean;
  theme: 'light' | 'dark';

  // Actions
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  toggleCitationPanel: () => void;
  setCitationPanelOpen: (open: boolean) => void;
  setCitationModalOpen: (open: boolean) => void;
  setTheme: (theme: 'light' | 'dark') => void;
  toggleTheme: () => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      isSidebarOpen: true,
      isCitationPanelOpen: true,
      isCitationModalOpen: false,
      theme: 'light',

      toggleSidebar: () =>
        set((state) => ({ isSidebarOpen: !state.isSidebarOpen })),

      setSidebarOpen: (isSidebarOpen) => set({ isSidebarOpen }),

      toggleCitationPanel: () =>
        set((state) => ({ isCitationPanelOpen: !state.isCitationPanelOpen })),

      setCitationPanelOpen: (isCitationPanelOpen) => set({ isCitationPanelOpen }),

      setCitationModalOpen: (isCitationModalOpen) => set({ isCitationModalOpen }),

      setTheme: (theme) => set({ theme }),

      toggleTheme: () =>
        set((state) => ({
          theme: state.theme === 'light' ? 'dark' : 'light',
        })),
    }),
    {
      name: 'shariacomply-ui',
      partialize: (state) => ({ theme: state.theme }),
    }
  )
);
