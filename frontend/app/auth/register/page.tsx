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

/**
 * Mirrors the backend validation rules
 * (backend/internal/auth/service.go validateRegisterRequest):
 * name 3-255 chars, phone ^\+?[0-9]{10,15}$, password 8-128 chars with
 * upper + lower + digit + symbol.
 */
const schema = z.object({
  name: z.string().trim().min(3, 'Min 3 characters').max(255, 'Max 255 characters'),
  email: z.email('Invalid email'),
  phone: z
    .string()
    .trim()
    .regex(/^\+?[0-9]{10,15}$/, 'Use 10-15 digits, optional leading +'),
  password: z
    .string()
    .min(8, 'Min 8 characters')
    .max(128, 'Max 128 characters')
    .regex(/[A-Z]/, 'Must contain an uppercase letter')
    .regex(/[a-z]/, 'Must contain a lowercase letter')
    .regex(/[0-9]/, 'Must contain a number')
    .regex(/[^A-Za-z0-9]/, 'Must contain a symbol'),
});

type RegisterValues = z.infer<typeof schema>;

export default function Register() {
  const router = useRouter();
  const registerAccount = useAuthStore((state) => state.register);
  const [error, setError] = useState('');
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<RegisterValues>({ resolver: zodResolver(schema) });

  const onSubmit = async (data: RegisterValues) => {
    setError('');
    try {
      await registerAccount(data);
      router.push('/auth/verify-otp');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Registration failed'));
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-[#FAFAFA] px-4 py-10">
      <div className="bg-white border border-[#E2E8F0] rounded-2xl shadow-sm p-8 w-full max-w-md">
        <h1 className="text-2xl font-bold tracking-tight text-[#171717]">
          Create account
        </h1>
        <p className="text-sm text-[#4D4D4D] mb-6 mt-1">
          Register to buy, transfer, and resell tickets
        </p>

        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <div>
            <label
              htmlFor="name"
              className="block text-sm font-medium text-[#171717] mb-1.5"
            >
              Full Name
            </label>
            <input
              id="name"
              autoComplete="name"
              aria-invalid={errors.name ? true : undefined}
              aria-describedby={errors.name ? 'name-error' : undefined}
              className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
              {...register('name')}
            />
            {errors.name && (
              <span
                id="name-error"
                role="alert"
                className="mt-1 block text-xs text-red-600"
              >
                {errors.name.message}
              </span>
            )}
          </div>

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
              htmlFor="phone"
              className="block text-sm font-medium text-[#171717] mb-1.5"
            >
              Phone
            </label>
            <input
              id="phone"
              type="tel"
              autoComplete="tel"
              placeholder="+62812345678"
              aria-invalid={errors.phone ? true : undefined}
              aria-describedby={errors.phone ? 'phone-error' : undefined}
              className="w-full border border-[#E2E8F0] rounded-lg px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-[#D08080] focus:border-transparent transition-all"
              {...register('phone')}
            />
            {errors.phone && (
              <span
                id="phone-error"
                role="alert"
                className="mt-1 block text-xs text-red-600"
              >
                {errors.phone.message}
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
              autoComplete="new-password"
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
            {isSubmitting ? 'Loading...' : 'Sign Up'}
          </button>
        </form>

        <p className="text-center mt-6 text-sm text-[#4D4D4D]">
          Already have an account?{' '}
          <Link
            href="/auth/login"
            className="font-medium text-[#D08080] hover:underline"
          >
            Login
          </Link>
        </p>
      </div>
    </div>
  );
}
