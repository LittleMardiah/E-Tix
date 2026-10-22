import React from 'react';

export type AlertVariant = 'error' | 'success' | 'info' | 'warning';

export interface AlertProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: AlertVariant;
  title?: string;
}

const variantStyles: Record<AlertVariant, string> = {
  error: 'bg-red-50 text-red-700 border-red-200',
  success: 'bg-green-50 text-green-700 border-green-200',
  info: 'bg-blue-50 text-blue-700 border-blue-200',
  warning: 'bg-amber-50 text-amber-800 border-amber-200',
};

export const Alert: React.FC<AlertProps> = ({
  variant = 'info',
  title,
  className = '',
  children,
  ...props
}) => {
  return (
    <div
      role={variant === 'error' ? 'alert' : 'status'}
      className={`rounded-md border p-3 text-sm ${variantStyles[variant]} ${className}`.trim()}
      {...props}
    >
      {title && <p className="font-semibold">{title}</p>}
      {children}
    </div>
  );
};
