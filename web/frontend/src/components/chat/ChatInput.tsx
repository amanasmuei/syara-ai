import { useState, useRef, useEffect, KeyboardEvent } from 'react';
import { Send, Paperclip } from 'lucide-react';
import { clsx } from 'clsx';

interface ChatInputProps {
  onSend: (message: string) => void;
  isLoading: boolean;
  placeholder?: string;
}

export function ChatInput({
  onSend,
  isLoading,
  placeholder = 'Ask about Islamic banking compliance...',
}: ChatInputProps) {
  const [message, setMessage] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Auto-resize textarea
  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
      textareaRef.current.style.height = `${Math.min(textareaRef.current.scrollHeight, 200)}px`;
    }
  }, [message]);

  const handleSubmit = () => {
    const trimmedMessage = message.trim();
    if (trimmedMessage && !isLoading) {
      onSend(trimmedMessage);
      setMessage('');
      if (textareaRef.current) {
        textareaRef.current.style.height = 'auto';
      }
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  return (
    <div className="max-w-4xl mx-auto">
      <div className="relative flex items-end gap-2 bg-gray-50 rounded-2xl border border-gray-200 focus-within:border-primary-400 focus-within:ring-2 focus-within:ring-primary-100 transition-all">
        {/* Attachment button (future feature) */}
        <button
          className="p-3 text-gray-400 hover:text-gray-600 transition-colors disabled:opacity-50"
          disabled={isLoading}
          aria-label="Attach file"
        >
          <Paperclip className="w-5 h-5" />
        </button>

        <textarea
          ref={textareaRef}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={isLoading}
          rows={1}
          className={clsx(
            'flex-1 py-3 pr-3 bg-transparent border-none resize-none focus:outline-none text-gray-900 placeholder:text-gray-400',
            'scrollbar-thin max-h-[200px]'
          )}
        />

        <button
          onClick={handleSubmit}
          disabled={!message.trim() || isLoading}
          className={clsx(
            'p-3 m-1 rounded-xl transition-all',
            message.trim() && !isLoading
              ? 'bg-primary-600 text-white hover:bg-primary-700'
              : 'bg-gray-200 text-gray-400 cursor-not-allowed'
          )}
          aria-label="Send message"
        >
          <Send className="w-5 h-5" />
        </button>
      </div>

      <p className="text-xs text-gray-400 text-center mt-2">
        Press Enter to send, Shift+Enter for new line
      </p>
    </div>
  );
}
