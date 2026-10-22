import React from 'react';

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  title?: string;
}

export const Card: React.FC<CardProps> = ({
  title,
  className = '',
  children,
  ...props
}) => {
  return (
    <div
      className={`rounded-lg border border-gray-200 bg-white shadow-sm ${className}`.trim()}
      {...props}
    >
      {title && (
        <div className="border-b border-gray-200 px-6 py-4">
          <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
        </div>
      )}
      <div className="p-6">{children}</div>
    </div>
  );
};
