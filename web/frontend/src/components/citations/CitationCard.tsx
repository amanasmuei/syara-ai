import { Eye, Download, FileText, ExternalLink } from 'lucide-react';
import { clsx } from 'clsx';
import type { Citation } from '@/types';

interface CitationCardProps {
  citation: Citation;
  index: number;
  onClick: () => void;
}

export function CitationCard({ citation, index, onClick }: CitationCardProps) {
  const sourceTypeColors = {
    bnm: 'bg-blue-100 text-blue-700',
    aaoifi: 'bg-green-100 text-green-700',
    other: 'bg-gray-100 text-gray-700',
  };

  const handleDownload = (e: React.MouseEvent, type: 'full' | 'page') => {
    e.stopPropagation();
    const url = type === 'full' ? citation.download.full_pdf : citation.download.single_page;
    window.open(url, '_blank');
  };

  return (
    <div
      onClick={onClick}
      className="card-hover overflow-hidden group"
    >
      {/* Citation Number Badge */}
      <div className="absolute top-3 left-3 z-10 w-7 h-7 bg-primary-600 text-white text-sm font-bold rounded-lg flex items-center justify-center shadow-md">
        {index}
      </div>

      {/* Thumbnail */}
      <div className="relative h-40 bg-gray-100 overflow-hidden">
        {citation.visual.thumbnail_url ? (
          <img
            src={citation.visual.thumbnail_url}
            alt={`Citation from ${citation.source.name}`}
            className="w-full h-full object-cover transition-transform duration-300 group-hover:scale-105"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <FileText className="w-16 h-16 text-gray-300" />
          </div>
        )}

        {/* Hover Overlay */}
        <div className="absolute inset-0 bg-black/0 group-hover:bg-black/40 transition-all duration-300 flex items-center justify-center opacity-0 group-hover:opacity-100">
          <button
            onClick={(e) => {
              e.stopPropagation();
              onClick();
            }}
            className="p-3 bg-white rounded-full shadow-lg hover:bg-gray-100 transition-colors"
          >
            <Eye className="w-5 h-5 text-gray-700" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="p-4">
        {/* Source Type Badge */}
        <div className="flex items-center gap-2 mb-2">
          <span
            className={clsx(
              'text-xs font-medium px-2 py-0.5 rounded-full uppercase',
              sourceTypeColors[citation.source.type]
            )}
          >
            {citation.source.type}
          </span>
          <span className="text-xs text-gray-400">
            Page {citation.source.page_number}
          </span>
        </div>

        {/* Source Name */}
        <h3 className="font-medium text-gray-900 text-sm mb-1 line-clamp-1">
          {citation.source.name}
        </h3>

        {/* Section */}
        {citation.source.section && (
          <p className="text-xs text-gray-500 mb-2">
            {citation.source.section}
          </p>
        )}

        {/* Content Snippet */}
        <p className="text-xs text-gray-600 line-clamp-3 mb-3">
          {citation.content_snippet}
        </p>

        {/* Relevance Score */}
        <div className="mb-3">
          <div className="flex items-center justify-between text-xs mb-1">
            <span className="text-gray-500">Relevance</span>
            <span className="text-gray-700 font-medium">
              {Math.round(citation.relevance * 100)}%
            </span>
          </div>
          <div className="h-1.5 bg-gray-200 rounded-full overflow-hidden">
            <div
              className="h-full bg-primary-500 rounded-full transition-all"
              style={{ width: `${citation.relevance * 100}%` }}
            />
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-2">
          <button
            onClick={(e) => {
              e.stopPropagation();
              onClick();
            }}
            className="flex-1 btn-ghost text-xs py-1.5 flex items-center justify-center gap-1"
          >
            <Eye className="w-3.5 h-3.5" />
            View
          </button>
          <button
            onClick={(e) => handleDownload(e, 'page')}
            className="flex-1 btn-ghost text-xs py-1.5 flex items-center justify-center gap-1"
          >
            <Download className="w-3.5 h-3.5" />
            Page
          </button>
          {citation.source.url && (
            <a
              href={citation.source.url}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="p-1.5 rounded-lg hover:bg-gray-100 transition-colors"
              aria-label="Open source website"
            >
              <ExternalLink className="w-4 h-4 text-gray-500" />
            </a>
          )}
        </div>
      </div>
    </div>
  );
}
