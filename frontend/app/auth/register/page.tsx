'use client';
import { useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import * as z from 'zod';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
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
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4 py-10">
      <Card className="w-full max-w-md">
        <h2 className="text-2xl font-bold text-gray-900 text-center mb-6">
          Create Account
        </h2>
        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <Input
            label="Full Name"
            autoComplete="name"
            {...register('name')}
            error={errors.name?.message}
          />
          <Input
            label="Email"
            type="email"
            autoComplete="email"
            {...register('email')}
            error={errors.email?.message}
          />
          <Input
            label="Phone"
            type="tel"
            autoComplete="tel"
            placeholder="+62812345678"
            {...register('phone')}
            error={errors.phone?.message}
          />
          <Input
            label="Password"
            type="password"
            autoComplete="new-password"
            {...register('password')}
            error={errors.password?.message}
          />
          <Button type="submit" className="w-full" isLoading={isSubmitting}>
            Sign Up
          </Button>
        </form>
        <p className="text-center mt-4 text-sm text-gray-600">
          Already have an account?{' '}
          <Link href="/auth/login" className="text-blue-600 hover:underline">
            Login
          </Link>
        </p>
      </Card>
    </div>
  );
}
