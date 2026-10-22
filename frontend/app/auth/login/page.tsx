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
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <Card className="w-full max-w-md">
        <h2 className="text-2xl font-bold text-gray-900 text-center mb-6">
          Welcome Back
        </h2>
        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <Input
            label="Email"
            type="email"
            autoComplete="email"
            {...register('email')}
            error={errors.email?.message}
          />
          <Input
            label="Password"
            type="password"
            autoComplete="current-password"
            {...register('password')}
            error={errors.password?.message}
          />
          <Button
            type="submit"
            className="w-full"
            isLoading={isSubmitting}
          >
            Login
          </Button>
        </form>
        <p className="text-center mt-4 text-sm text-gray-600">
          Don&apos;t have an account?{' '}
          <Link href="/auth/register" className="text-blue-600 hover:underline">
            Sign up
          </Link>
        </p>
      </Card>
    </div>
  );
}
