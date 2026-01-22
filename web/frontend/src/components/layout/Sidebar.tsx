import { MessageSquare, Clock, Trash2 } from 'lucide-react';
import { clsx } from 'clsx';
import { useUIStore } from '@/stores/uiStore';
import type { Conversation } from '@/types';

interface SidebarProps {
  conversations?: Conversation[];
  activeConversationId?: string | null;
  onSelectConversation?: (id: string) => void;
  onDeleteConversation?: (id: string) => void;
}

export function Sidebar({
  conversations = [],
  activeConversationId,
  onSelectConversation,
  onDeleteConversation,
}: SidebarProps) {
  const { isSidebarOpen, setSidebarOpen } = useUIStore();

  const formatDate = (date: Date | string) => {
    const dateObj = typeof date === 'string' ? new Date(date) : date;
    const now = new Date();
    const diff = now.getTime() - dateObj.getTime();
    const days = Math.floor(diff / (1000 * 60 * 60 * 24));

    if (days === 0) return 'Today';
    if (days === 1) return 'Yesterday';
    if (days < 7) return `${days} days ago`;
    return dateObj.toLocaleDateString();
  };

  return (
    <>
      {/* Mobile overlay */}
      {isSidebarOpen && (
        <div
          className="fixed inset-0 bg-black/50 z-40 lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside
        className={clsx(
          'fixed lg:static inset-y-0 left-0 z-50 w-72 bg-gray-50 border-r border-gray-200 flex flex-col transition-transform duration-300 lg:translate-x-0',
          isSidebarOpen ? 'translate-x-0' : '-translate-x-full'
        )}
      >
        <div className="p-4 border-b border-gray-200">
          <h2 className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
            Conversations
          </h2>
        </div>

        <div className="flex-1 overflow-y-auto scrollbar-thin p-2">
          {conversations.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full text-center px-4">
              <MessageSquare className="w-12 h-12 text-gray-300 mb-3" />
              <p className="text-gray-500 text-sm">No conversations yet</p>
              <p className="text-gray-400 text-xs mt-1">
                Start a new chat to get help with Islamic banking compliance
              </p>
            </div>
          ) : (
            <div className="space-y-1">
              {conversations.map((conversation) => (
                <div
                  key={conversation.id}
                  className={clsx(
                    'group flex items-start gap-3 p-3 rounded-lg cursor-pointer transition-colors',
                    activeConversationId === conversation.id
                      ? 'bg-primary-50 border border-primary-200'
                      : 'hover:bg-gray-100'
                  )}
                  onClick={() => onSelectConversation?.(conversation.id)}
                >
                  <MessageSquare
                    className={clsx(
                      'w-5 h-5 mt-0.5 flex-shrink-0',
                      activeConversationId === conversation.id
                        ? 'text-primary-600'
                        : 'text-gray-400'
                    )}
                  />
                  <div className="flex-1 min-w-0">
                    <p
                      className={clsx(
                        'text-sm font-medium truncate',
                        activeConversationId === conversation.id
                          ? 'text-primary-700'
                          : 'text-gray-700'
                      )}
                    >
                      {conversation.title}
                    </p>
                    <div className="flex items-center gap-1 mt-1">
                      <Clock className="w-3 h-3 text-gray-400" />
                      <span className="text-xs text-gray-400">
                        {formatDate(conversation.updated_at)}
                      </span>
                    </div>
                  </div>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      onDeleteConversation?.(conversation.id);
                    }}
                    className="p-1 rounded opacity-0 group-hover:opacity-100 hover:bg-gray-200 transition-all"
                    aria-label="Delete conversation"
                  >
                    <Trash2 className="w-4 h-4 text-gray-400 hover:text-red-500" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="p-4 border-t border-gray-200">
          <div className="text-xs text-gray-400 text-center">
            <p>Powered by SyaRA - AI</p>
          </div>
        </div>
      </aside>
    </>
  );
}
