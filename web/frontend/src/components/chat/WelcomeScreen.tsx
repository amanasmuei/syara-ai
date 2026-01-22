import { BookOpen, Scale, FileText, HelpCircle } from 'lucide-react';

interface WelcomeScreenProps {
  onSuggestionClick: (suggestion: string) => void;
}

const suggestions = [
  {
    icon: Scale,
    title: 'Compare Standards',
    query: 'Compare the Murabaha requirements between BNM regulations and AAOIFI Shariah standards.',
  },
  {
    icon: BookOpen,
    title: 'IIFA Resolutions',
    query: 'What are the IIFA (Majma Fiqh) resolutions on contemporary Islamic finance contracts?',
  },
  {
    icon: FileText,
    title: 'SC Malaysia Guidelines',
    query: 'What are the Securities Commission Malaysia requirements for Sukuk issuance?',
  },
  {
    icon: HelpCircle,
    title: 'Latest Regulations',
    query: 'What are the latest regulatory circulars from BNM on Islamic banking?',
  },
];

export function WelcomeScreen({ onSuggestionClick }: WelcomeScreenProps) {
  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8">
      <div className="max-w-2xl w-full text-center">
        {/* Title */}
        <div className="mb-8">
          <h1 className="text-3xl font-bold text-gray-900 mb-2">
            SyaRA - AI
          </h1>
          <p className="text-lg text-gray-600">
            Your Shariah Regulatory AI Assistant for Islamic Finance Compliance
          </p>
        </div>

        {/* Suggestion Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {suggestions.map((suggestion, index) => (
            <button
              key={index}
              onClick={() => onSuggestionClick(suggestion.query)}
              className="card-hover p-4 text-left group"
            >
              <div className="flex items-start gap-3">
                <div className="p-2 bg-gray-100 rounded-lg group-hover:bg-primary-100 transition-colors">
                  <suggestion.icon className="w-5 h-5 text-gray-600 group-hover:text-primary-600 transition-colors" />
                </div>
                <div>
                  <h3 className="font-medium text-gray-900 group-hover:text-primary-700 transition-colors">
                    {suggestion.title}
                  </h3>
                  <p className="text-sm text-gray-500 mt-1 line-clamp-2">
                    {suggestion.query}
                  </p>
                </div>
              </div>
            </button>
          ))}
        </div>

        {/* Disclaimer */}
        <p className="text-xs text-gray-400 mt-8">
          Always verify compliance decisions with certified Sharia scholars. This tool provides guidance based on BNM (Bank Negara Malaysia), SC (Securities Commission Malaysia), IIFA (International Islamic Fiqh Academy), and Malaysian State Fatwa Councils.
        </p>
      </div>
    </div>
  );
}
