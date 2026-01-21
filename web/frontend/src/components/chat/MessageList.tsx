import { MessageBubble } from './MessageBubble';
import type { Message, Citation } from '@/types';

interface MessageListProps {
  messages: Message[];
  onCitationClick?: (citation: Citation) => void;
}

export function MessageList({ messages, onCitationClick }: MessageListProps) {
  return (
    <div className="max-w-4xl mx-auto px-4 py-6 space-y-6">
      {messages.map((message) => (
        <MessageBubble
          key={message.id}
          message={message}
          onCitationClick={onCitationClick}
        />
      ))}
    </div>
  );
}
