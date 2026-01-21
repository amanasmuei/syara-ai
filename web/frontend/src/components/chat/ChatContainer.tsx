import { useRef, useEffect } from 'react';
import { MessageList } from './MessageList';
import { ChatInput } from './ChatInput';
import { WelcomeScreen } from './WelcomeScreen';
import { useChatStore } from '@/stores/chatStore';
import { useSendMessage } from '@/hooks/useChat';
import type { Citation } from '@/types';

interface ChatContainerProps {
  onCitationClick?: (citation: Citation) => void;
}

export function ChatContainer({ onCitationClick }: ChatContainerProps) {
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const { messages, isLoading } = useChatStore();
  const sendMessage = useSendMessage();

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  };

  useEffect(() => {
    scrollToBottom();
  }, [messages]);

  const handleSend = (content: string) => {
    sendMessage.mutate(content);
  };

  const handleSuggestionClick = (suggestion: string) => {
    sendMessage.mutate(suggestion);
  };

  return (
    <div className="flex flex-col h-full bg-white">
      {messages.length === 0 ? (
        <WelcomeScreen onSuggestionClick={handleSuggestionClick} />
      ) : (
        <div className="flex-1 overflow-y-auto scrollbar-thin">
          <MessageList
            messages={messages}
            onCitationClick={onCitationClick}
          />
          <div ref={messagesEndRef} />
        </div>
      )}

      <div className="border-t border-gray-200 p-4">
        <ChatInput
          onSend={handleSend}
          isLoading={isLoading}
        />
      </div>
    </div>
  );
}
