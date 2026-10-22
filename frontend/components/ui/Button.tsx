import React from 'react';
import Link from 'next/link';

type ButtonVariant = 'primary' | 'secondary' | 'outline';

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  isLoading?: boolean;
  /**
   * When provided the component renders a Next.js <Link> styled as a button.
   * Nesting a <button> inside an <a> is invalid HTML and breaks keyboard
   * navigation, so links must use this prop instead of wrapping.
   */
  href?: string;
}

const baseStyle =
  'inline-flex items-center justify-center gap-2 px-4 py-2 rounded-md font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed';

const variants: Record<ButtonVariant, string> = {
  primary: 'bg-blue-600 text-white hover:bg-blue-700 focus:ring-blue-500',
  secondary: 'bg-gray-200 text-gray-800 hover:bg-gray-300 focus:ring-gray-400',
  outline:
    'border border-gray-300 text-gray-700 hover:bg-gray-50 focus:ring-blue-500',
};

export const Button: React.FC<ButtonProps> = ({
  children,
  variant = 'primary',
  isLoading = false,
  href,
  className = '',
  disabled,
  type = 'button',
  ...props
}) => {
  const classes = `${baseStyle} ${variants[variant]} ${
    isLoading ? 'opacity-50 cursor-not-allowed' : ''
  } ${className}`.trim();

  const content = isLoading ? 'Loading...' : children;

  if (href) {
    return (
      <Link
        href={href}
        className={classes}
        aria-disabled={isLoading || disabled ? true : undefined}
        onClick={isLoading || disabled ? (e) => e.preventDefault() : undefined}
      >
        {content}
      </Link>
    );
  }

  return (
    <button
      type={type}
      className={classes}
      disabled={isLoading || Boolean(disabled)}
      {...props}
    >
      {content}
    </button>
  );
};
