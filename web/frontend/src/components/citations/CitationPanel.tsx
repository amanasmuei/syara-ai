import { X, BookOpen } from 'lucide-react';
import { clsx } from 'clsx';
import { CitationCard } from './CitationCard';
import { useUIStore } from '@/stores/uiStore';
import type { Citation } from '@/types';

interface CitationPanelProps {
  citations: Citation[];
  onCitationSelect: (citation: Citation) => void;
}

export function CitationPanel({ citations, onCitationSelect }: CitationPanelProps) {
  const { isCitationPanelOpen, setCitationPanelOpen } = useUIStore();

  if (!isCitationPanelOpen) {
    return (
      <button
        onClick={() => setCitationPanelOpen(true)}
        className="fixed right-4 top-1/2 -translate-y-1/2 p-3 bg-white rounded-l-lg shadow-lg border border-r-0 border-gray-200 hover:bg-gray-50 transition-colors z-30"
        aria-label="Open citations panel"
      >
        <BookOpen className="w-5 h-5 text-gray-600" />
        {citations.length > 0 && (
          <span className="absolute -top-2 -left-2 w-5 h-5 bg-primary-600 text-white text-xs rounded-full flex items-center justify-center">
            {citations.length}
          </span>
        )}
      </button>
    );
  }

  return (
    <aside
      className={clsx(
        'w-80 lg:w-96 bg-gray-50 border-l border-gray-200 flex flex-col h-full transition-all duration-300'
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200 bg-white">
        <div className="flex items-center gap-2">
          <BookOpen className="w-5 h-5 text-primary-600" />
          <h2 className="font-semibold text-gray-900">Sources</h2>
          {citations.length > 0 && (
            <span className="px-2 py-0.5 bg-primary-100 text-primary-700 text-xs rounded-full font-medium">
              {citations.length}
            </span>
          )}
        </div>
        <button
          onClick={() => setCitationPanelOpen(false)}
          className="p-1.5 rounded-lg hover:bg-gray-100 transition-colors"
          aria-label="Close citations panel"
        >
          <X className="w-4 h-4 text-gray-500" />
        </button>
      </div>

      {/* Citation List */}
      <div className="flex-1 overflow-y-auto scrollbar-thin p-4 space-y-4">
        {citations.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-center px-4">
            <BookOpen className="w-12 h-12 text-gray-300 mb-3" />
            <p className="text-gray-500 text-sm">No citations yet</p>
            <p className="text-gray-400 text-xs mt-1">
              Citations from responses will appear here
            </p>
          </div>
        ) : (
          citations.map((citation, index) => (
            <CitationCard
              key={citation.id}
              citation={citation}
              index={index + 1}
              onClick={() => onCitationSelect(citation)}
            />
          ))
        )}
      </div>
    </aside>
  );
}
