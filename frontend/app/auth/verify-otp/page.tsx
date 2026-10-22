'use client';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import * as z from 'zod';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { apiClient, getApiErrorMessage } from '@/lib/api/client';
import {
  clearPendingVerification,
  readPendingVerification,
} from '@/lib/stores/authStore';

const schema = z.object({
  code: z
    .string()
    .length(6, 'Code must be 6 digits')
    .regex(/^[0-9]+$/, 'Code must be numeric'),
});

type OtpValues = z.infer<typeof schema>;

export default function VerifyOTP() {
  const router = useRouter();
  const [error, setError] = useState('');
  const [email, setEmail] = useState('');
  const [hasSession, setHasSession] = useState(true);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<OtpValues>({ resolver: zodResolver(schema) });

  useEffect(() => {
    const pending = readPendingVerification();
    if (!pending) {
      setHasSession(false);
      return;
    }
    setEmail(pending.email);
  }, []);

  const onSubmit = async (data: OtpValues) => {
    setError('');
    const pending = readPendingVerification();
    if (!pending) {
      setHasSession(false);
      return;
    }
    try {
      await apiClient.post('/auth/verify-otp', {
        user_id: pending.user_id,
        code: data.code,
      });
      clearPendingVerification();
      router.push('/customer/dashboard');
    } catch (err) {
      setError(getApiErrorMessage(err, 'Verification failed'));
    }
  };

  if (!hasSession) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
        <Card className="w-full max-w-md text-center">
          <h2 className="text-2xl font-bold text-gray-900 mb-4">
            Verify Your Email
          </h2>
          <Alert variant="warning" className="mb-4 text-left">
            No pending registration found. Please register or log in first.
          </Alert>
          <div className="flex gap-3 justify-center">
            <Button href="/auth/register" variant="outline">
              Register
            </Button>
            <Button href="/auth/login">Login</Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <Card className="w-full max-w-md">
        <h2 className="text-2xl font-bold text-gray-900 text-center mb-2">
          Verify Your Email
        </h2>
        <p className="text-sm text-gray-600 text-center mb-6">
          {email
            ? `We sent a 6-digit code to ${email}.`
            : 'We sent a 6-digit code to your email.'}
        </p>
        {error && (
          <Alert variant="error" className="mb-4">
            {error}
          </Alert>
        )}
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4" noValidate>
          <Input
            label="6-digit Code"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            placeholder="000000"
            {...register('code')}
            error={errors.code?.message}
          />
          <Button type="submit" className="w-full" isLoading={isSubmitting}>
            Verify
          </Button>
        </form>
      </Card>
    </div>
  );
}
