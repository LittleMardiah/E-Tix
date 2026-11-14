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
  'inline-flex items-center justify-center gap-2 px-4 py-2 rounded-md font-medium transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed';

const variants: Record<ButtonVariant, string> = {
  primary: 'bg-[#D08080] text-[#171717] hover:bg-[#c06b6b] focus:ring-[#D08080]',
  secondary: 'bg-[#FAFAFA] text-[#171717] hover:bg-[#F2F2F2] focus:ring-[#D08080]',
  outline:
    'border border-[#E2E8F0] text-[#171717] hover:bg-[#FAFAFA] focus:ring-[#D08080]',
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
