import { useMemo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { User, Bot, BookOpen } from 'lucide-react';
import { clsx } from 'clsx';
import { LoadingDots } from '@/components/common';
import type { Message, Citation } from '@/types';

interface MessageBubbleProps {
  message: Message;
  onCitationClick?: (citation: Citation) => void;
}

export function MessageBubble({ message, onCitationClick }: MessageBubbleProps) {
  const isUser = message.role === 'user';

  // Parse citation markers [1], [2], etc. from content
  const processedContent = useMemo(() => {
    if (!message.citations?.length) return message.content;

    // Replace [n] markers with clickable spans
    let content = message.content;
    message.citations.forEach((citation, index) => {
      const marker = `[${index + 1}]`;
      content = content.replace(
        new RegExp(`\\[${index + 1}\\]`, 'g'),
        `<citation-marker data-index="${index}">[${index + 1}]</citation-marker>`
      );
    });
    return content;
  }, [message.content, message.citations]);

  const handleCitationMarkerClick = (index: number) => {
    if (message.citations && message.citations[index]) {
      onCitationClick?.(message.citations[index]);
    }
  };

  return (
    <div
      className={clsx(
        'flex gap-4 animate-slide-up',
        isUser ? 'flex-row-reverse' : 'flex-row'
      )}
    >
      {/* Avatar */}
      <div
        className={clsx(
          'w-10 h-10 rounded-full flex items-center justify-center flex-shrink-0',
          isUser ? 'bg-primary-100' : 'bg-gray-100'
        )}
      >
        {isUser ? (
          <User className="w-5 h-5 text-primary-600" />
        ) : (
          <Bot className="w-5 h-5 text-gray-600" />
        )}
      </div>

      {/* Message Content */}
      <div
        className={clsx(
          'flex-1 max-w-[80%] rounded-2xl px-4 py-3',
          isUser
            ? 'bg-primary-600 text-white'
            : 'bg-gray-100 text-gray-900'
        )}
      >
        {message.isLoading ? (
          <div className="flex items-center gap-2 py-2">
            <LoadingDots />
            <span className="text-sm text-gray-500">Thinking...</span>
          </div>
        ) : (
          <>
            <div
              className={clsx(
                'prose prose-sm max-w-none',
                isUser ? 'prose-invert' : '',
                '[&_citation-marker]:inline-flex [&_citation-marker]:items-center [&_citation-marker]:justify-center [&_citation-marker]:w-5 [&_citation-marker]:h-5 [&_citation-marker]:text-xs [&_citation-marker]:font-medium [&_citation-marker]:rounded [&_citation-marker]:cursor-pointer [&_citation-marker]:transition-colors',
                isUser
                  ? '[&_citation-marker]:bg-primary-500 [&_citation-marker]:hover:bg-primary-400'
                  : '[&_citation-marker]:bg-primary-100 [&_citation-marker]:text-primary-700 [&_citation-marker]:hover:bg-primary-200'
              )}
              onClick={(e) => {
                const target = e.target as HTMLElement;
                if (target.tagName.toLowerCase() === 'citation-marker') {
                  const index = parseInt(target.dataset.index || '0', 10);
                  handleCitationMarkerClick(index);
                }
              }}
            >
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  // Custom rendering for better styling
                  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
                  ul: ({ children }) => (
                    <ul className="list-disc list-inside mb-2 space-y-1">{children}</ul>
                  ),
                  ol: ({ children }) => (
                    <ol className="list-decimal list-inside mb-2 space-y-1">{children}</ol>
                  ),
                  code: ({ className, children, ...props }) => {
                    const isInline = !className;
                    return isInline ? (
                      <code
                        className={clsx(
                          'px-1.5 py-0.5 rounded text-sm font-mono',
                          isUser ? 'bg-primary-500' : 'bg-gray-200'
                        )}
                        {...props}
                      >
                        {children}
                      </code>
                    ) : (
                      <code
                        className={clsx(
                          'block p-3 rounded-lg text-sm font-mono overflow-x-auto',
                          isUser ? 'bg-primary-700' : 'bg-gray-800 text-gray-100'
                        )}
                        {...props}
                      >
                        {children}
                      </code>
                    );
                  },
                }}
              >
                {processedContent}
              </ReactMarkdown>
            </div>

            {/* Citation Summary */}
            {message.citations && message.citations.length > 0 && (
              <div
                className={clsx(
                  'mt-3 pt-3 border-t flex flex-wrap items-center gap-2',
                  isUser ? 'border-primary-500' : 'border-gray-200'
                )}
              >
                <BookOpen
                  className={clsx(
                    'w-4 h-4',
                    isUser ? 'text-primary-200' : 'text-gray-400'
                  )}
                />
                <span
                  className={clsx(
                    'text-xs',
                    isUser ? 'text-primary-200' : 'text-gray-500'
                  )}
                >
                  Sources:
                </span>
                {message.citations.map((citation, index) => (
                  <button
                    key={citation.id}
                    onClick={() => onCitationClick?.(citation)}
                    className={clsx(
                      'text-xs px-2 py-1 rounded transition-colors',
                      isUser
                        ? 'bg-primary-500 hover:bg-primary-400 text-white'
                        : 'bg-gray-200 hover:bg-gray-300 text-gray-700'
                    )}
                  >
                    [{index + 1}] {citation.source.name}
                  </button>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
