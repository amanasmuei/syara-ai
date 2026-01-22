import { useCallback, useMemo } from 'react';
import { Layout } from '@/components/layout';
import { ChatContainer } from '@/components/chat';
import { CitationPanel, CitationModal } from '@/components/citations';
import { SettingsPanel } from '@/components/settings/SettingsPanel';
import { useChatStore } from '@/stores/chatStore';
import { useUIStore } from '@/stores/uiStore';
import {
  useConversations,
  useConversation,
  useDeleteConversation,
  useRealtimeUpdates,
} from '@/hooks';
import type { Citation } from '@/types';

function App() {
  const { messages, selectedCitation, setSelectedCitation, conversationId, setConversationId } =
    useChatStore();
  const { isCitationModalOpen, setCitationModalOpen, isCitationPanelOpen } = useUIStore();

  // Fetch conversations
  const { data: conversations = [] } = useConversations();

  // Fetch selected conversation
  useConversation(conversationId);

  // Delete conversation mutation
  const deleteConversation = useDeleteConversation();

  // Real-time updates
  useRealtimeUpdates({ enabled: true });

  // Extract all citations from messages
  const allCitations = useMemo(() => {
    const citations: Citation[] = [];
    messages.forEach((message) => {
      if (message.citations) {
        message.citations.forEach((citation) => {
          if (!citations.find((c) => c.id === citation.id)) {
            citations.push(citation);
          }
        });
      }
    });
    return citations;
  }, [messages]);

  const handleCitationClick = useCallback(
    (citation: Citation) => {
      setSelectedCitation(citation);
      setCitationModalOpen(true);
    },
    [setSelectedCitation, setCitationModalOpen]
  );

  const handleCitationModalClose = useCallback(() => {
    setCitationModalOpen(false);
  }, [setCitationModalOpen]);

  const handleCitationNavigate = useCallback(
    (citation: Citation) => {
      setSelectedCitation(citation);
    },
    [setSelectedCitation]
  );

  const handleSelectConversation = useCallback(
    (id: string) => {
      setConversationId(id);
    },
    [setConversationId]
  );

  const handleDeleteConversation = useCallback(
    (id: string) => {
      if (confirm('Are you sure you want to delete this conversation?')) {
        deleteConversation.mutate(id);
      }
    },
    [deleteConversation]
  );

  return (
    <Layout
      conversations={conversations}
      activeConversationId={conversationId}
      onSelectConversation={handleSelectConversation}
      onDeleteConversation={handleDeleteConversation}
    >
      <div className="flex h-full">
        {/* Main Chat Area */}
        <div className="flex-1 min-w-0">
          <ChatContainer onCitationClick={handleCitationClick} />
        </div>

        {/* Citation Panel */}
        {isCitationPanelOpen && (
          <CitationPanel
            citations={allCitations}
            onCitationSelect={handleCitationClick}
          />
        )}
      </div>

      {/* Citation Modal */}
      <CitationModal
        citation={selectedCitation}
        isOpen={isCitationModalOpen}
        onClose={handleCitationModalClose}
        citations={allCitations}
        onNavigate={handleCitationNavigate}
      />

      {/* Settings Panel */}
      <SettingsPanel />
    </Layout>
  );
}

export default App;
