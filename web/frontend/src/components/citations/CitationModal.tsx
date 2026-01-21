import { useState } from 'react';
import { X, Download, Copy, ExternalLink, Check, Eye, EyeOff, FileText, ChevronLeft, ChevronRight } from 'lucide-react';
import { clsx } from 'clsx';
import toast from 'react-hot-toast';
import { Modal } from '@/components/common';
import type { Citation } from '@/types';

interface CitationModalProps {
  citation: Citation | null;
  isOpen: boolean;
  onClose: () => void;
  citations?: Citation[];
  onNavigate?: (citation: Citation) => void;
}

export function CitationModal({
  citation,
  isOpen,
  onClose,
  citations = [],
  onNavigate,
}: CitationModalProps) {
  const [showHighlighted, setShowHighlighted] = useState(true);
  const [copied, setCopied] = useState(false);

  if (!citation) return null;

  const currentIndex = citations.findIndex((c) => c.id === citation.id);
  const hasPrev = currentIndex > 0;
  const hasNext = currentIndex < citations.length - 1;

  const handleCopy = async () => {
    const text = `${citation.content_snippet}\n\nSource: ${citation.source.name}, Page ${citation.source.page_number}${citation.source.section ? `, ${citation.source.section}` : ''}`;

    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success('Citation copied to clipboard');
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error('Failed to copy citation');
    }
  };

  const handleDownload = (type: 'full' | 'page') => {
    const url = type === 'full' ? citation.download.full_pdf : citation.download.single_page;
    window.open(url, '_blank');
  };

  const handleNavigate = (direction: 'prev' | 'next') => {
    if (direction === 'prev' && hasPrev) {
      onNavigate?.(citations[currentIndex - 1]);
    } else if (direction === 'next' && hasNext) {
      onNavigate?.(citations[currentIndex + 1]);
    }
  };

  const sourceTypeColors = {
    bnm: 'bg-blue-100 text-blue-700 border-blue-200',
    aaoifi: 'bg-green-100 text-green-700 border-green-200',
    other: 'bg-gray-100 text-gray-700 border-gray-200',
  };

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title=""
      size="full"
      showCloseButton={false}
    >
      <div className="flex flex-col lg:flex-row h-full">
        {/* Image Section */}
        <div className="flex-1 bg-gray-900 relative flex items-center justify-center p-4 lg:p-8">
          {/* Close Button */}
          <button
            onClick={onClose}
            className="absolute top-4 right-4 p-2 bg-white/10 hover:bg-white/20 rounded-lg transition-colors z-10"
            aria-label="Close modal"
          >
            <X className="w-5 h-5 text-white" />
          </button>

          {/* Navigation Arrows */}
          {citations.length > 1 && (
            <>
              <button
                onClick={() => handleNavigate('prev')}
                disabled={!hasPrev}
                className={clsx(
                  'absolute left-4 top-1/2 -translate-y-1/2 p-3 rounded-full transition-all z-10',
                  hasPrev
                    ? 'bg-white/10 hover:bg-white/20 text-white'
                    : 'bg-white/5 text-white/30 cursor-not-allowed'
                )}
                aria-label="Previous citation"
              >
                <ChevronLeft className="w-6 h-6" />
              </button>
              <button
                onClick={() => handleNavigate('next')}
                disabled={!hasNext}
                className={clsx(
                  'absolute right-4 top-1/2 -translate-y-1/2 p-3 rounded-full transition-all z-10',
                  hasNext
                    ? 'bg-white/10 hover:bg-white/20 text-white'
                    : 'bg-white/5 text-white/30 cursor-not-allowed'
                )}
                aria-label="Next citation"
              >
                <ChevronRight className="w-6 h-6" />
              </button>
            </>
          )}

          {/* Image */}
          <div className="relative max-w-full max-h-full">
            {citation.visual.thumbnail_url || citation.visual.highlighted_image_url ? (
              <img
                src={
                  showHighlighted
                    ? citation.visual.highlighted_image_url || citation.visual.thumbnail_url
                    : citation.visual.original_image_url || citation.visual.thumbnail_url
                }
                alt={`Citation from ${citation.source.name}`}
                className="max-w-full max-h-[70vh] object-contain rounded-lg shadow-2xl"
              />
            ) : (
              <div className="w-96 h-96 bg-gray-800 rounded-lg flex items-center justify-center">
                <FileText className="w-24 h-24 text-gray-600" />
              </div>
            )}
          </div>

          {/* Toggle Highlight Button */}
          {citation.visual.highlighted_image_url && citation.visual.original_image_url && (
            <button
              onClick={() => setShowHighlighted(!showHighlighted)}
              className="absolute bottom-4 left-1/2 -translate-x-1/2 flex items-center gap-2 px-4 py-2 bg-white/10 hover:bg-white/20 rounded-full text-white text-sm transition-colors"
            >
              {showHighlighted ? (
                <>
                  <EyeOff className="w-4 h-4" />
                  Show Original
                </>
              ) : (
                <>
                  <Eye className="w-4 h-4" />
                  Show Highlighted
                </>
              )}
            </button>
          )}

          {/* Page Counter */}
          {citations.length > 1 && (
            <div className="absolute bottom-4 right-4 px-3 py-1.5 bg-white/10 rounded-full text-white text-sm">
              {currentIndex + 1} / {citations.length}
            </div>
          )}
        </div>

        {/* Details Section */}
        <div className="w-full lg:w-96 bg-white flex flex-col">
          {/* Header */}
          <div className="p-6 border-b border-gray-200">
            <div className="flex items-center gap-2 mb-3">
              <span
                className={clsx(
                  'text-xs font-medium px-2.5 py-1 rounded-full uppercase border',
                  sourceTypeColors[citation.source.type]
                )}
              >
                {citation.source.type}
              </span>
              <span className="text-sm text-gray-500">
                Page {citation.source.page_number}
              </span>
            </div>
            <h2 className="text-xl font-semibold text-gray-900 mb-1">
              {citation.source.name}
            </h2>
            {citation.source.section && (
              <p className="text-sm text-gray-500">{citation.source.section}</p>
            )}
          </div>

          {/* Content */}
          <div className="flex-1 overflow-y-auto p-6">
            <div className="mb-6">
              <h3 className="text-sm font-medium text-gray-700 mb-2">
                Relevant Content
              </h3>
              <div className="bg-gray-50 rounded-lg p-4 border border-gray-200">
                <p className="text-sm text-gray-700 leading-relaxed">
                  {citation.content_snippet}
                </p>
              </div>
            </div>

            {/* Relevance Score */}
            <div className="mb-6">
              <h3 className="text-sm font-medium text-gray-700 mb-2">
                Relevance Score
              </h3>
              <div className="flex items-center gap-3">
                <div className="flex-1 h-2.5 bg-gray-200 rounded-full overflow-hidden">
                  <div
                    className="h-full bg-primary-500 rounded-full"
                    style={{ width: `${citation.relevance * 100}%` }}
                  />
                </div>
                <span className="text-sm font-semibold text-gray-900">
                  {Math.round(citation.relevance * 100)}%
                </span>
              </div>
            </div>
          </div>

          {/* Actions */}
          <div className="p-6 border-t border-gray-200 space-y-3">
            <div className="grid grid-cols-2 gap-3">
              <button
                onClick={() => handleDownload('page')}
                className="btn-primary flex items-center justify-center gap-2"
              >
                <Download className="w-4 h-4" />
                This Page
              </button>
              <button
                onClick={() => handleDownload('full')}
                className="btn-secondary flex items-center justify-center gap-2"
              >
                <Download className="w-4 h-4" />
                Full PDF
              </button>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <button
                onClick={handleCopy}
                className="btn-ghost flex items-center justify-center gap-2"
              >
                {copied ? (
                  <>
                    <Check className="w-4 h-4 text-green-600" />
                    Copied!
                  </>
                ) : (
                  <>
                    <Copy className="w-4 h-4" />
                    Copy Citation
                  </>
                )}
              </button>
              {citation.source.url && (
                <a
                  href={citation.source.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="btn-ghost flex items-center justify-center gap-2"
                >
                  <ExternalLink className="w-4 h-4" />
                  Source
                </a>
              )}
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
