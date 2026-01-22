import { X, Database, FileText, RefreshCw, Globe, CheckCircle, XCircle, Clock, Loader2, Upload } from 'lucide-react';
import { clsx } from 'clsx';
import { useUIStore } from '@/stores/uiStore';
import type { SettingsTab, CrawlerSource, UploadedFile, CrawlerJob } from '@/types';

// Mock data - replace with actual API calls
const mockCrawlers: CrawlerSource[] = [
  {
    id: '1',
    name: 'Bank Negara Malaysia',
    type: 'bnm',
    baseUrl: 'https://www.bnm.gov.my',
    description: 'Central Bank of Malaysia - Islamic Banking Policies & Guidelines',
    status: 'active',
    lastCrawled: new Date('2025-01-22T10:30:00'),
    documentCount: 156,
  },
  {
    id: '2',
    name: 'Securities Commission Malaysia',
    type: 'sc',
    baseUrl: 'https://www.sc.com.my',
    description: 'Capital Market Regulations & Islamic Capital Market',
    status: 'active',
    lastCrawled: new Date('2025-01-22T08:15:00'),
    documentCount: 89,
  },
  {
    id: '3',
    name: 'IIFA (International Islamic Fiqh Academy)',
    type: 'iifa',
    baseUrl: 'https://iifa-aifi.org',
    description: 'Fiqh Resolutions & Islamic Jurisprudence',
    status: 'active',
    lastCrawled: new Date('2025-01-21T14:00:00'),
    documentCount: 267,
  },
  {
    id: '4',
    name: 'Malaysian State Fatwa Councils',
    type: 'fatwa',
    baseUrl: 'Various State Portals',
    description: 'Fatwa from 14 Malaysian States',
    status: 'active',
    lastCrawled: new Date('2025-01-20T09:45:00'),
    documentCount: 1243,
  },
];

const mockFiles: UploadedFile[] = [
  {
    id: '1',
    filename: 'AAOIFI_Shariah_Standards_2024.pdf',
    fileType: 'application/pdf',
    fileSize: 15728640,
    uploadedAt: new Date('2025-01-15T10:00:00'),
    status: 'indexed',
    pageCount: 450,
    source: 'Manual Upload',
  },
  {
    id: '2',
    filename: 'BNM_Murabaha_Guidelines.pdf',
    fileType: 'application/pdf',
    fileSize: 2097152,
    uploadedAt: new Date('2025-01-18T14:30:00'),
    status: 'indexed',
    pageCount: 32,
    source: 'BNM Crawler',
  },
  {
    id: '3',
    filename: 'Internal_Policy_Draft.pdf',
    fileType: 'application/pdf',
    fileSize: 524288,
    uploadedAt: new Date('2025-01-22T09:00:00'),
    status: 'processing',
    pageCount: 12,
    source: 'Manual Upload',
  },
];

const mockJobs: CrawlerJob[] = [
  {
    id: '1',
    crawlerType: 'BNM',
    status: 'completed',
    startedAt: new Date('2025-01-22T10:00:00'),
    completedAt: new Date('2025-01-22T10:30:00'),
    documentsProcessed: 15,
    documentsTotal: 15,
  },
  {
    id: '2',
    crawlerType: 'SC',
    status: 'running',
    startedAt: new Date('2025-01-22T11:00:00'),
    documentsProcessed: 23,
    documentsTotal: 50,
  },
  {
    id: '3',
    crawlerType: 'IIFA',
    status: 'pending',
    documentsProcessed: 0,
    documentsTotal: 0,
  },
  {
    id: '4',
    crawlerType: 'Fatwa',
    status: 'failed',
    startedAt: new Date('2025-01-21T08:00:00'),
    completedAt: new Date('2025-01-21T08:05:00'),
    documentsProcessed: 5,
    documentsTotal: 100,
    errorMessage: 'Connection timeout to Selangor Fatwa portal',
  },
];

const tabs: { id: SettingsTab; label: string; icon: typeof Database }[] = [
  { id: 'crawlers', label: 'Crawler Sources', icon: Globe },
  { id: 'files', label: 'Uploaded Files', icon: FileText },
  { id: 'jobs', label: 'Crawler Jobs', icon: RefreshCw },
];

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / 1048576).toFixed(1) + ' MB';
}

function formatDate(date: Date): string {
  return new Intl.DateTimeFormat('en-MY', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date);
}

function StatusBadge({ status }: { status: string }) {
  const config: Record<string, { color: string; icon: typeof CheckCircle }> = {
    active: { color: 'bg-green-100 text-green-700', icon: CheckCircle },
    inactive: { color: 'bg-gray-100 text-gray-600', icon: XCircle },
    error: { color: 'bg-red-100 text-red-700', icon: XCircle },
    indexed: { color: 'bg-green-100 text-green-700', icon: CheckCircle },
    processing: { color: 'bg-yellow-100 text-yellow-700', icon: Loader2 },
    failed: { color: 'bg-red-100 text-red-700', icon: XCircle },
    completed: { color: 'bg-green-100 text-green-700', icon: CheckCircle },
    running: { color: 'bg-blue-100 text-blue-700', icon: Loader2 },
    pending: { color: 'bg-gray-100 text-gray-600', icon: Clock },
  };

  const { color, icon: Icon } = config[status] || config.inactive;

  return (
    <span className={clsx('inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs font-medium', color)}>
      <Icon className={clsx('w-3 h-3', status === 'running' || status === 'processing' ? 'animate-spin' : '')} />
      {status.charAt(0).toUpperCase() + status.slice(1)}
    </span>
  );
}

function CrawlersTab() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-500">
          {mockCrawlers.length} sources configured
        </p>
        <button className="btn-primary text-sm py-1.5 px-3">
          <RefreshCw className="w-4 h-4 mr-1" />
          Crawl All
        </button>
      </div>

      <div className="space-y-3">
        {mockCrawlers.map((crawler) => (
          <div
            key={crawler.id}
            className="bg-white border border-gray-200 rounded-lg p-4 hover:border-primary-300 transition-colors"
          >
            <div className="flex items-start justify-between">
              <div className="flex-1">
                <div className="flex items-center gap-2">
                  <h4 className="font-medium text-gray-900">{crawler.name}</h4>
                  <StatusBadge status={crawler.status} />
                </div>
                <p className="text-sm text-gray-500 mt-1">{crawler.description}</p>
                <p className="text-xs text-gray-400 mt-2">
                  <Globe className="w-3 h-3 inline mr-1" />
                  {crawler.baseUrl}
                </p>
              </div>
              <div className="text-right text-sm">
                <p className="font-medium text-gray-900">{crawler.documentCount}</p>
                <p className="text-xs text-gray-500">documents</p>
              </div>
            </div>
            {crawler.lastCrawled && (
              <p className="text-xs text-gray-400 mt-3 pt-3 border-t border-gray-100">
                Last crawled: {formatDate(crawler.lastCrawled)}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function FilesTab() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-500">
          {mockFiles.length} files uploaded
        </p>
        <button className="btn-primary text-sm py-1.5 px-3">
          <Upload className="w-4 h-4 mr-1" />
          Upload File
        </button>
      </div>

      <div className="space-y-2">
        {mockFiles.map((file) => (
          <div
            key={file.id}
            className="bg-white border border-gray-200 rounded-lg p-3 hover:border-primary-300 transition-colors"
          >
            <div className="flex items-center gap-3">
              <div className="p-2 bg-gray-100 rounded-lg">
                <FileText className="w-5 h-5 text-gray-600" />
              </div>
              <div className="flex-1 min-w-0">
                <p className="font-medium text-gray-900 truncate">{file.filename}</p>
                <div className="flex items-center gap-3 text-xs text-gray-500 mt-1">
                  <span>{formatFileSize(file.fileSize)}</span>
                  {file.pageCount && <span>{file.pageCount} pages</span>}
                  <span>{file.source}</span>
                </div>
              </div>
              <div className="text-right">
                <StatusBadge status={file.status} />
                <p className="text-xs text-gray-400 mt-1">{formatDate(file.uploadedAt)}</p>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function JobsTab() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-500">
          {mockJobs.filter((j) => j.status === 'running').length} jobs running
        </p>
        <button className="btn-secondary text-sm py-1.5 px-3">
          View History
        </button>
      </div>

      <div className="space-y-3">
        {mockJobs.map((job) => (
          <div
            key={job.id}
            className="bg-white border border-gray-200 rounded-lg p-4"
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={clsx(
                  'p-2 rounded-lg',
                  job.status === 'running' ? 'bg-blue-100' :
                  job.status === 'completed' ? 'bg-green-100' :
                  job.status === 'failed' ? 'bg-red-100' : 'bg-gray-100'
                )}>
                  <Database className={clsx(
                    'w-5 h-5',
                    job.status === 'running' ? 'text-blue-600' :
                    job.status === 'completed' ? 'text-green-600' :
                    job.status === 'failed' ? 'text-red-600' : 'text-gray-600'
                  )} />
                </div>
                <div>
                  <h4 className="font-medium text-gray-900">{job.crawlerType} Crawler</h4>
                  {job.startedAt && (
                    <p className="text-xs text-gray-500">Started: {formatDate(job.startedAt)}</p>
                  )}
                </div>
              </div>
              <StatusBadge status={job.status} />
            </div>

            {(job.status === 'running' || job.status === 'completed') && (
              <div className="mt-3">
                <div className="flex items-center justify-between text-xs text-gray-500 mb-1">
                  <span>Progress</span>
                  <span>{job.documentsProcessed} / {job.documentsTotal}</span>
                </div>
                <div className="w-full bg-gray-200 rounded-full h-2">
                  <div
                    className={clsx(
                      'h-2 rounded-full transition-all',
                      job.status === 'completed' ? 'bg-green-500' : 'bg-blue-500'
                    )}
                    style={{
                      width: job.documentsTotal > 0
                        ? `${(job.documentsProcessed / job.documentsTotal) * 100}%`
                        : '0%'
                    }}
                  />
                </div>
              </div>
            )}

            {job.errorMessage && (
              <div className="mt-3 p-2 bg-red-50 rounded text-xs text-red-700">
                {job.errorMessage}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export function SettingsPanel() {
  const { isSettingsPanelOpen, setSettingsPanelOpen, settingsActiveTab, setSettingsActiveTab } = useUIStore();

  if (!isSettingsPanelOpen) return null;

  return (
    <>
      {/* Overlay */}
      <div
        className="fixed inset-0 bg-black/50 z-50"
        onClick={() => setSettingsPanelOpen(false)}
      />

      {/* Panel */}
      <div className="fixed inset-y-0 right-0 w-full max-w-lg bg-gray-50 shadow-xl z-50 flex flex-col animate-slide-in-right">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 bg-white border-b border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900">Settings</h2>
          <button
            onClick={() => setSettingsPanelOpen(false)}
            className="p-2 rounded-lg hover:bg-gray-100 transition-colors"
            aria-label="Close settings"
          >
            <X className="w-5 h-5 text-gray-500" />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-gray-200 bg-white px-6">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setSettingsActiveTab(tab.id)}
              className={clsx(
                'flex items-center gap-2 px-4 py-3 text-sm font-medium border-b-2 transition-colors',
                settingsActiveTab === tab.id
                  ? 'border-primary-500 text-primary-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              )}
            >
              <tab.icon className="w-4 h-4" />
              {tab.label}
            </button>
          ))}
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6">
          {settingsActiveTab === 'crawlers' && <CrawlersTab />}
          {settingsActiveTab === 'files' && <FilesTab />}
          {settingsActiveTab === 'jobs' && <JobsTab />}
        </div>
      </div>
    </>
  );
}
