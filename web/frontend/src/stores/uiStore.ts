import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { SettingsTab } from '@/types';

interface UIState {
  isSidebarOpen: boolean;
  isCitationPanelOpen: boolean;
  isCitationModalOpen: boolean;
  isSettingsPanelOpen: boolean;
  settingsActiveTab: SettingsTab;
  theme: 'light' | 'dark';

  // Actions
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  toggleCitationPanel: () => void;
  setCitationPanelOpen: (open: boolean) => void;
  setCitationModalOpen: (open: boolean) => void;
  toggleSettingsPanel: () => void;
  setSettingsPanelOpen: (open: boolean) => void;
  setSettingsActiveTab: (tab: SettingsTab) => void;
  setTheme: (theme: 'light' | 'dark') => void;
  toggleTheme: () => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      isSidebarOpen: true,
      isCitationPanelOpen: true,
      isCitationModalOpen: false,
      isSettingsPanelOpen: false,
      settingsActiveTab: 'crawlers' as SettingsTab,
      theme: 'light',

      toggleSidebar: () =>
        set((state) => ({ isSidebarOpen: !state.isSidebarOpen })),

      setSidebarOpen: (isSidebarOpen) => set({ isSidebarOpen }),

      toggleCitationPanel: () =>
        set((state) => ({ isCitationPanelOpen: !state.isCitationPanelOpen })),

      setCitationPanelOpen: (isCitationPanelOpen) => set({ isCitationPanelOpen }),

      setCitationModalOpen: (isCitationModalOpen) => set({ isCitationModalOpen }),

      toggleSettingsPanel: () =>
        set((state) => ({ isSettingsPanelOpen: !state.isSettingsPanelOpen })),

      setSettingsPanelOpen: (isSettingsPanelOpen) => set({ isSettingsPanelOpen }),

      setSettingsActiveTab: (settingsActiveTab) => set({ settingsActiveTab }),

      setTheme: (theme) => set({ theme }),

      toggleTheme: () =>
        set((state) => ({
          theme: state.theme === 'light' ? 'dark' : 'light',
        })),
    }),
    {
      name: 'syara-ai-ui',
      partialize: (state) => ({ theme: state.theme }),
    }
  )
);
