import { BookOpen, Scale, FileText, HelpCircle } from 'lucide-react';

interface WelcomeScreenProps {
  onSuggestionClick: (suggestion: string) => void;
}

const suggestions = [
  {
    icon: Scale,
    title: 'Murabaha Requirements',
    query: 'What are the key requirements for a Murabaha transaction according to BNM and AAOIFI standards?',
  },
  {
    icon: BookOpen,
    title: 'Ijara Guidelines',
    query: 'Explain the Ijara (leasing) guidelines under AAOIFI Sharia Standards.',
  },
  {
    icon: FileText,
    title: 'Sukuk Compliance',
    query: 'What are the Sharia compliance requirements for issuing Sukuk?',
  },
  {
    icon: HelpCircle,
    title: 'Musharakah vs Mudarabah',
    query: 'What is the difference between Musharakah and Mudarabah contracts?',
  },
];

export function WelcomeScreen({ onSuggestionClick }: WelcomeScreenProps) {
  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8">
      <div className="max-w-2xl w-full text-center">
        {/* Logo and Title */}
        <div className="mb-8">
          <div className="w-20 h-20 bg-primary-100 rounded-2xl flex items-center justify-center mx-auto mb-4">
            <Scale className="w-10 h-10 text-primary-600" />
          </div>
          <h1 className="text-3xl font-bold text-gray-900 mb-2">
            ShariaComply AI
          </h1>
          <p className="text-lg text-gray-600">
            Your intelligent assistant for Islamic banking compliance
          </p>
        </div>

        {/* Features */}
        <div className="mb-8 flex flex-wrap justify-center gap-4 text-sm text-gray-500">
          <div className="flex items-center gap-2">
            <div className="w-2 h-2 bg-primary-500 rounded-full" />
            <span>BNM Guidelines</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-2 h-2 bg-primary-500 rounded-full" />
            <span>AAOIFI Standards</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-2 h-2 bg-primary-500 rounded-full" />
            <span>Visual Citations</span>
          </div>
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
          Always verify compliance decisions with certified Sharia scholars. This tool provides guidance based on BNM and AAOIFI documentation.
        </p>
      </div>
    </div>
  );
}
