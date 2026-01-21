import { Menu, Plus, Settings, Moon, Sun } from 'lucide-react';
import { useUIStore } from '@/stores/uiStore';
import { useChatStore } from '@/stores/chatStore';

export function Header() {
  const { theme, toggleTheme, toggleSidebar } = useUIStore();
  const { clearChat } = useChatStore();

  const handleNewChat = () => {
    clearChat();
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
            <span className="text-white font-bold text-sm">SC</span>
          </div>
          <div className="hidden sm:block">
            <h1 className="text-lg font-semibold text-gray-900">ShariaComply AI</h1>
            <p className="text-xs text-gray-500">Islamic Banking Compliance Assistant</p>
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
          onClick={toggleTheme}
          className="p-2 rounded-lg hover:bg-gray-100 transition-colors"
          aria-label="Toggle theme"
        >
          {theme === 'light' ? (
            <Moon className="w-5 h-5 text-gray-600" />
          ) : (
            <Sun className="w-5 h-5 text-gray-600" />
          )}
        </button>

        <button
          className="p-2 rounded-lg hover:bg-gray-100 transition-colors"
          aria-label="Settings"
        >
          <Settings className="w-5 h-5 text-gray-600" />
        </button>
      </div>
    </header>
  );
}
