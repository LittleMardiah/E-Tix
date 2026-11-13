'use client';
import { useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import * as z from 'zod';
import { Alert } from '@/components/ui/Alert';
import { getApiErrorMessage } from '@/lib/api/client';
import { useAuthStore } from '@/lib/stores/authStore';

const schema = z.object({
  email: z.email('Invalid email'),
  password: z.string().min(8, 'Min 8 characters'),
});

type LoginValues = z.infer<typeof schema>;

export default function Login() {
  const router = useRouter();
  const login = useAuthStore((state) => state.login);
  const [error, setError] = useState('');
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginValues>({ resolver: zodResolver(schema) });

  const onSubmit = async (data: LoginValues) => {
    setError('');
    try {
      await login({ email: data.email, password: data.password });
      router.push('/customer/dashboard');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Login failed'));
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-[#FAFAFA] px-4">
      <div className="bg-white border border-[#E2E8F0] rounded-2xl shadow-sm p-8 w-full max-w-md">
        <h1 className="text-2xl font-bold tracking-tight text-[#171717]">
          Welcome back
        </h1>
        <p className="text-sm text-[#4D4D4D] mb-6 mt-1">
          Enter your credentials to access your account
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <div>
            <label
              htmlFor="email"
              className="block text-sm font-medium text-[#171717] mb-1.5"
            >
              Email
            </label>
            <input
              id="email"
              type="email"
              autoComplete="email"
              aria-invalid={errors.email ? true : undefined}
              aria-describedby={errors.email ? 'email-error' : undefined}
              className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
              {...register('email')}
            />
            {errors.email && (
              <span
                id="email-error"
                role="alert"
                className="mt-1 block text-xs text-red-600"
              >
                {errors.email.message}
              </span>
            )}
          </div>

          <div>
            <label
              htmlFor="password"
              className="block text-sm font-medium text-[#171717] mb-1.5"
            >
              Password
            </label>
            <input
              id="password"
              type="password"
              autoComplete="current-password"
              aria-invalid={errors.password ? true : undefined}
              aria-describedby={errors.password ? 'password-error' : undefined}
              className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
              {...register('password')}
            />
            {errors.password && (
              <span
                id="password-error"
                role="alert"
                className="mt-1 block text-xs text-red-600"
              >
                {errors.password.message}
              </span>
            )}
          </div>

          <button
            type="submit"
            disabled={isSubmitting}
            className="w-full bg-[#D08080] text-[#171717] font-medium rounded-lg py-2.5 hover:bg-[#c06b6b] transition-colors mt-6 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {isSubmitting ? 'Loading...' : 'Login'}
          </button>
        </form>

        <p className="text-center mt-6 text-sm text-[#4D4D4D]">
          Don&apos;t have an account?{' '}
          <Link
            href="/auth/register"
            className="font-medium text-[#D08080] hover:underline"
          >
            Sign up
          </Link>
        </p>
      </div>
    </div>
  );
}
