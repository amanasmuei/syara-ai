import { ReactNode } from 'react';
import { Header } from './Header';
import { Sidebar } from './Sidebar';
import type { Conversation } from '@/types';

interface LayoutProps {
  children: ReactNode;
  conversations?: Conversation[];
  activeConversationId?: string | null;
  onSelectConversation?: (id: string) => void;
  onDeleteConversation?: (id: string) => void;
}

export function Layout({
  children,
  conversations,
  activeConversationId,
  onSelectConversation,
  onDeleteConversation,
}: LayoutProps) {
  return (
    <div className="h-screen flex flex-col bg-gray-50">
      <Header />
      <div className="flex-1 flex overflow-hidden">
        <Sidebar
          conversations={conversations}
          activeConversationId={activeConversationId}
          onSelectConversation={onSelectConversation}
          onDeleteConversation={onDeleteConversation}
        />
        <main className="flex-1 overflow-hidden">{children}</main>
      </div>
    </div>
  );
}
