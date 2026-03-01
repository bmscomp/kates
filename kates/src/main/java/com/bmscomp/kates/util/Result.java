package com.bmscomp.kates.util;

import java.util.Optional;
import java.util.function.Consumer;
import java.util.function.Function;
import java.util.function.Supplier;

/**
 * Functional Result monad pattern (Either L R) to represent success or failure
 * explicitly instead of using exception-based control flow.
 */
public sealed interface Result<T, E> permits Result.Success, Result.Failure {

    boolean isSuccess();
    boolean isFailure();
    
    Optional<T> asSuccess();
    Optional<E> asFailure();

    <U> Result<U, E> map(Function<? super T, ? extends U> mapper);
    <U> Result<U, E> flatMap(Function<? super T, Result<U, E>> mapper);
    
    T orElse(T defaultValue);
    T orElseGet(Supplier<? extends T> defaultSupplier);
    T orElseThrow(Function<? super E, ? extends RuntimeException> exceptionSupplier);

    void ifSuccess(Consumer<? super T> action);
    void ifFailure(Consumer<? super E> action);

    // Factory methods
    static <T, E> Result<T, E> success(T value) {
        return new Success<>(value);
    }

    static <T, E> Result<T, E> failure(E error) {
        return new Failure<>(error);
    }

    // Pattern Matching over Records (Java 14+)
    record Success<T, E>(T value) implements Result<T, E> {
        @Override public boolean isSuccess() { return true; }
        @Override public boolean isFailure() { return false; }
        @Override public Optional<T> asSuccess() { return Optional.of(value); }
        @Override public Optional<E> asFailure() { return Optional.empty(); }
        
        @Override public <U> Result<U, E> map(Function<? super T, ? extends U> mapper) {
            return Result.success(mapper.apply(value));
        }

        @Override public <U> Result<U, E> flatMap(Function<? super T, Result<U, E>> mapper) {
            return mapper.apply(value);
        }

        @Override public T orElse(T defaultValue) { return value; }
        @Override public T orElseGet(Supplier<? extends T> defaultSupplier) { return value; }
        @Override public T orElseThrow(Function<? super E, ? extends RuntimeException> exceptionSupplier) { return value; }

        @Override public void ifSuccess(Consumer<? super T> action) { action.accept(value); }
        @Override public void ifFailure(Consumer<? super E> action) {}
    }

    record Failure<T, E>(E error) implements Result<T, E> {
        @Override public boolean isSuccess() { return false; }
        @Override public boolean isFailure() { return true; }
        @Override public Optional<T> asSuccess() { return Optional.empty(); }
        @Override public Optional<E> asFailure() { return Optional.of(error); }

        @Override @SuppressWarnings("unchecked")
        public <U> Result<U, E> map(Function<? super T, ? extends U> mapper) {
            return (Result<U, E>) this;
        }

        @Override @SuppressWarnings("unchecked")
        public <U> Result<U, E> flatMap(Function<? super T, Result<U, E>> mapper) {
            return (Result<U, E>) this;
        }

        @Override public T orElse(T defaultValue) { return defaultValue; }
        @Override public T orElseGet(Supplier<? extends T> defaultSupplier) { return defaultSupplier.get(); }
        @Override public T orElseThrow(Function<? super E, ? extends RuntimeException> exceptionSupplier) {
            RuntimeException ex = exceptionSupplier.apply(error);
            throw (ex != null) ? ex : new RuntimeException("Unknown error: " + error);
        }

        @Override public void ifSuccess(Consumer<? super T> action) {}
        @Override public void ifFailure(Consumer<? super E> action) { action.accept(error); }
    }
}
