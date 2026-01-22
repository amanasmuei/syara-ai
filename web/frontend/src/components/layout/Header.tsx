import { Menu, Plus, Settings } from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { useUIStore } from '@/stores/uiStore';
import { useChatStore } from '@/stores/chatStore';
import { chatKeys } from '@/hooks/useChat';

export function Header() {
  const queryClient = useQueryClient();
  const { toggleSidebar, toggleSettingsPanel } = useUIStore();
  const { clearChat } = useChatStore();

  const handleNewChat = () => {
    clearChat();
    // Refresh conversations list to show the previous conversation in sidebar
    queryClient.invalidateQueries({ queryKey: chatKeys.conversations() });
  };

  return (
    <header className="h-16 bg-white border-b border-gray-200 flex items-center justify-between px-4 lg:px-6">
      <div className="flex items-center gap-3">
        <button
          onClick={toggleSidebar}
          className="p-2 rounded-lg hover:bg-gray-100 transition-colors lg:hidden"
          aria-label="Toggle sidebar"
        >
          <Menu className="w-5 h-5 text-gray-600" />
        </button>

        <div className="flex items-center gap-2">
          <div className="w-8 h-8 bg-primary-600 rounded-lg flex items-center justify-center">
            <span className="text-white font-bold text-sm">S</span>
          </div>
          <div className="hidden sm:block">
            <h1 className="text-lg font-semibold text-gray-900">SyaRA - AI</h1>
          </div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          onClick={handleNewChat}
          className="btn-primary flex items-center gap-2"
        >
          <Plus className="w-4 h-4" />
          <span className="hidden sm:inline">New Chat</span>
        </button>

        <button
          onClick={toggleSettingsPanel}
          className="p-2 rounded-lg hover:bg-gray-100 transition-colors"
          aria-label="Settings"
        >
          <Settings className="w-5 h-5 text-gray-600" />
        </button>
      </div>
    </header>
  );
}
