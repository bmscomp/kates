package com.bmscomp.kates.domain;

import jakarta.validation.ConstraintValidator;
import jakarta.validation.ConstraintValidatorContext;

/** Holds a {@link Rate} to its limits. */
public class RateValidator implements ConstraintValidator<Rate, Integer> {

    /** Whether the producers can honour this rate: -1, which is unlimited, or 1 or more. */
    public static boolean allows(int rate) {
        return rate == -1 || rate >= 1;
    }

    @Override
    public boolean isValid(Integer rate, ConstraintValidatorContext context) {
        return rate == null || allows(rate);
    }
}
